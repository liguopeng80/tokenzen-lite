package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/liguopeng80/tokenzen-lite/server/internal/domain"
)

// 流式响应中途中断：已产生的 token 照常结算，但用量日志要留下中断标记。
// 没有标记时这条调用与正常结束的完全一样，员工反映「回答被截断」时无从确认。
func TestRelayStreamAbortMarkedInUsageLog(t *testing.T) {
	e := newTestEnv(t)
	// 上游发出内容与 usage 后直接断开连接，不发 [DONE] 也不正常收尾。
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, `data: {"id":"c","model":"glm-5","choices":[{"delta":{"content":"你"}}]}`+"\n\n")
		fl.Flush()
		fmt.Fprint(w, `data: {"id":"c","model":"glm-5","choices":[],`+
			`"usage":{"prompt_tokens":200,"completion_tokens":50}}`+"\n\n")
		fl.Flush()
		// 立刻切断底层连接，制造读取中断。
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("测试服务器应支持 Hijack")
			return
		}
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("Hijack 失败: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "streamabort", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-abort", upstream.URL, 0, []string{"glm-5"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("流式请求失败: %v", err)
	}
	// 读完可读部分即可，中断本身由服务端感知。
	_, _ = readAllQuiet(resp)

	log := e.waitUsageLog(t, userID)
	if log.ErrorClass != domain.ErrClassStreamAborted {
		t.Fatalf("中断的流式调用应标记为 %s，实际 %q（消息：%s）",
			domain.ErrClassStreamAborted, log.ErrorClass, log.ErrorMessage)
	}
	if log.ErrorMessage == "" {
		t.Error("应记录中断原因，便于管理员判断是上游问题还是客户端问题")
	}
	// 已产生的 token 照常结算：状态仍是已结算，不是失败也不是退款。
	if log.Status != domain.UsageSettled {
		t.Errorf("中断不改变结算结果，状态应为已结算，实际 %s", log.Status)
	}
	if log.CreditsCharged != 300 {
		t.Errorf("应按上游返回的 usage 结算 300 积分，实际 %d", log.CreditsCharged)
	}
	e.assertReconcile(t)
}

// 正常结束的流式调用不带任何异常标记，避免标记本身失去区分度。
func TestRelayStreamNormalEndHasNoErrorClass(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		fmt.Fprint(w, `data: {"id":"c","model":"glm-5","choices":[],`+
			`"usage":{"prompt_tokens":200,"completion_tokens":50}}`+"\n\n")
		fl.Flush()
		fmt.Fprint(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "streamnormal", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-normal", upstream.URL, 0, []string{"glm-5"}, nil)

	payload, _ := json.Marshal(map[string]any{
		"model": "glm-5", "stream": true,
		"messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest("POST", e.srv.URL+"/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("流式请求失败: %v", err)
	}
	_, _ = readAllQuiet(resp)

	log := e.waitUsageLog(t, userID)
	if log.ErrorClass != domain.ErrClassNone {
		t.Errorf("正常结束不应带异常分类，实际 %q", log.ErrorClass)
	}
}

// readAllQuiet 读完响应体并关闭，忽略中途的连接错误——本组用例故意制造中断。
func readAllQuiet(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, err := buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}

// 上游返回较长的中文错误信息时，这条调用仍要能写进用量日志。
// 按字节硬切会把汉字切成半个，PostgreSQL 对非法 UTF-8 直接拒绝整条写入——
// 表现是管理员看到员工报障却在日志里找不到对应记录。
func TestRelayLongChineseUpstreamErrorStillLogged(t *testing.T) {
	e := newTestEnv(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// 每字三字节，500 字节的切点必然落在字符中间。
		long := ""
		for i := 0; i < 200; i++ {
			long += "模型调用失败，请检查输入内容是否符合要求。"
		}
		fmt.Fprintf(w, `{"error":{"message":%q}}`, long)
	}))
	defer upstream.Close()

	userID, key := e.seedRelayUser(t, "longchineseerr", 1_000_000, nil)
	e.seedModel(t, "glm-5")
	e.seedChannel(t, "ch-longerr", upstream.URL, 0, []string{"glm-5"}, nil)

	status, _ := e.postV1(t, key, "/v1/chat/completions", map[string]any{
		"model": "glm-5", "messages": []map[string]any{{"role": "user", "content": "hi"}},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("上游 400 应透传，实际 %d", status)
	}

	log := e.waitUsageLog(t, userID)
	if log.ErrorMessage == "" {
		t.Fatal("应记录上游错误信息")
	}
	if !utf8.ValidString(log.ErrorMessage) {
		t.Fatalf("落库的错误信息必须是合法 UTF-8：%q", log.ErrorMessage)
	}
	if len(log.ErrorMessage) > 500 {
		t.Errorf("错误信息应收敛到 500 字节以内，实际 %d", len(log.ErrorMessage))
	}
	e.assertReconcile(t)
}
