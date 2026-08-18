package api

import (
	"testing"
	"time"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
	"github.com/liguopeng80/tokenzen-lite/server/internal/store"
)

// 覆盖两处上线前整改：兑换码的过期展示态（管理端列表与筛选），
// 以及核销失败时给员工的具体原因。

// seedRedemptionRow 直接入库一个兑换码，返回其 ID。
func (e *testEnv) seedRedemptionRow(t *testing.T, codeHash, name string,
	status domain.RedemptionStatus, expiresAt *time.Time) int64 {

	t.Helper()
	red := &store.Redemption{
		BatchID: "expiry-test", CodeHash: codeHash, Name: name,
		Credits: 1000, Status: status, ExpiresAt: expiresAt,
	}
	if err := e.db.Create(red).Error; err != nil {
		t.Fatalf("种入兑换码失败: %v", err)
	}
	return red.ID
}

// 管理端列表必须把已过期的码显示为「已过期」而不是「未使用」：
// 后者会让管理员以为这批码还能发下去，员工拿到手却兑不了。
func TestAdminRedemptionExpiredStatusAndFilter(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "expiryroot", domain.RoleRoot)

	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(time.Hour)
	e.seedRedemptionRow(t, "hash-expired", "已过期批次", domain.RedemptionUnused, &past)
	e.seedRedemptionRow(t, "hash-valid", "有效期内批次", domain.RedemptionUnused, &future)
	e.seedRedemptionRow(t, "hash-forever", "永不过期批次", domain.RedemptionUnused, nil)
	e.seedRedemptionRow(t, "hash-disabled", "已作废批次", domain.RedemptionDisabled, &past)

	statusByName := func(t *testing.T, query string) map[string]string {
		t.Helper()
		resp, env := e.do(t, rootC, "GET", "/api/admin/redemptions/"+query, nil)
		if resp.StatusCode != 200 {
			t.Fatalf("查询 %q 应 200，实际 %d %v", query, resp.StatusCode, env)
		}
		out := map[string]string{}
		for _, it := range pageItems(t, env) {
			row := it.(map[string]any)
			out[row["name"].(string)] = row["effective_status"].(string)
		}
		return out
	}

	all := statusByName(t, "")
	if got := all["已过期批次"]; got != string(domain.RedemptionExpired) {
		t.Errorf("过期的码展示态应为 expired，实际 %q", got)
	}
	if got := all["有效期内批次"]; got != string(domain.RedemptionUnused) {
		t.Errorf("有效期内的码展示态应为 unused，实际 %q", got)
	}
	if got := all["永不过期批次"]; got != string(domain.RedemptionUnused) {
		t.Errorf("未设过期时间的码展示态应为 unused，实际 %q", got)
	}
	if got := all["已作废批次"]; got != string(domain.RedemptionDisabled) {
		t.Errorf("已作废的码不应因过期时间改判，实际 %q", got)
	}

	// unused 与 expired 是「未使用」的两个互斥子集，相加等于库里全部 unused 行，
	// 管理员按状态筛选时既不会漏也不会重。
	expired := statusByName(t, "?status=expired")
	if len(expired) != 1 || expired["已过期批次"] == "" {
		t.Errorf("按 expired 筛选应只得到已过期批次，实际 %v", expired)
	}
	unused := statusByName(t, "?status=unused")
	if len(unused) != 2 || unused["已过期批次"] != "" {
		t.Errorf("按 unused 筛选应得到 2 个未过期批次且不含已过期，实际 %v", unused)
	}
	disabled := statusByName(t, "?status=disabled")
	if len(disabled) != 1 || disabled["已作废批次"] == "" {
		t.Errorf("按 disabled 筛选应只得到已作废批次，实际 %v", disabled)
	}
}

// 核销失败的提示必须说清原因：过期与作废只能找管理员，抄错了才值得重试。
func TestRedeemFailureMessages(t *testing.T) {
	e := newTestEnv(t)
	rootC := e.seedAndLogin(t, "redeemroot", domain.RoleRoot)
	userC := e.seedAndLogin(t, "redeemuser", domain.RoleUser)

	// 明文只在生成时返回一次，因此从生成接口取真实的码。
	newCode := func(t *testing.T, name string) string {
		t.Helper()
		resp, env := e.do(t, rootC, "POST", "/api/admin/redemptions/batch",
			map[string]any{"count": 1, "credits": 1000, "name": name})
		if resp.StatusCode != 201 {
			t.Fatalf("生成批次应 201，实际 %d %v", resp.StatusCode, env)
		}
		codes := env["data"].(map[string]any)["codes"].([]any)
		return codes[0].(string)
	}

	expiredCode := newCode(t, "过期码")
	if err := e.db.Model(&store.Redemption{}).Where("name = ?", "过期码").
		Update("expires_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatalf("改写过期时间失败: %v", err)
	}
	disabledCode := newCode(t, "作废码")
	if err := e.db.Model(&store.Redemption{}).Where("name = ?", "作废码").
		Update("status", domain.RedemptionDisabled).Error; err != nil {
		t.Fatalf("改写状态失败: %v", err)
	}
	usedCode := newCode(t, "重复码")

	resp, env := e.do(t, userC, "POST", "/api/me/redeem", map[string]any{"code": usedCode})
	if resp.StatusCode != 200 {
		t.Fatalf("首次核销应 200，实际 %d %v", resp.StatusCode, env)
	}

	cases := []struct{ name, code, want string }{
		{"已过期", expiredCode, "兑换码已过期，请联系管理员重新发放"},
		{"已作废", disabledCode, "兑换码已被作废，请联系管理员"},
		{"已使用", usedCode, "兑换码已被使用"},
		{"不存在", "tzr-never-issued-code", "兑换码不存在，请核对后重新输入"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp, env := e.do(t, userC, "POST", "/api/me/redeem", map[string]any{"code": c.code})
			if resp.StatusCode != 400 {
				t.Fatalf("核销失败应 400，实际 %d %v", resp.StatusCode, env)
			}
			if msg, _ := env["message"].(string); msg != c.want {
				t.Errorf("提示应为 %q，实际 %q", c.want, msg)
			}
		})
	}

	// 只有首次核销入账：四次失败不得改动余额。
	uid := e.userIDByName(t, "redeemuser")
	var u store.User
	if err := e.db.First(&u, uid).Error; err != nil {
		t.Fatalf("查用户失败: %v", err)
	}
	if u.CreditBalance != 1000 {
		t.Errorf("余额应只含首次核销的 1000 积分，实际 %d", u.CreditBalance)
	}
}
