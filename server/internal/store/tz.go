package store

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 本文件集中所有服务器时区与「本地自然日/月」相关的纯函数助手，供 SQL 侧分桶、
// JSON 序列化与 handler 端日期解析共用。生产常见 PostgreSQL 用 UTC、应用用本地时区，
// 因此日期边界一律按服务器本地时区显式换算，不依赖数据库会话时区。

// dayKeyLayout 是按日聚合 group_key 的 Go 时间布局，与 rollup.go 按日维度的 SQL
// 表达式 to_char(src.day, 'YYYY-MM-DD') 一一对应。改动任一侧必须同步另一侧。
const dayKeyLayout = "2006-01-02"

// SpendDay 返回给定时刻所属的自然日（服务器时区），作为计数的分桶键。
func SpendDay(t time.Time) time.Time {
	local := t.In(time.Local)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.Local)
}

// MonthRange 返回 t 所在自然月的起点与下月起点（左闭右开，服务器时区）。
func MonthRange(t time.Time) (time.Time, time.Time) {
	local := t.In(time.Local)
	from := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.Local)
	return from, from.AddDate(0, 1, 0)
}

// ParseDayKey 把按日聚合的 group_key（"YYYY-MM-DD"，服务器时区）解析回 time.Time。
// 与 rollup.go 按日维度的 to_char 表达式配对，收口 SQL↔Go 的字符串往返。
func ParseDayKey(s string) (time.Time, error) {
	return time.ParseInLocation(dayKeyLayout, s, time.Local)
}

// localWallClock 把 time.Time 的挂钟数字（年月日时分秒）重新挂在 time.Local 上。
// 用于把 SQL 侧 (created_at AT TIME ZONE zone) 计算出的本地挂钟——被 pgx 按 UTC
// 标注——还原为本地时区，确保 JSON 输出携带本地偏移、前端按本地小时显示。
func localWallClock(t time.Time) time.Time {
	return time.Date(
		t.Year(), t.Month(), t.Day(),
		t.Hour(), t.Minute(), t.Second(), t.Nanosecond(),
		time.Local,
	)
}

// LocalZoneName 返回服务器本地时区的 IANA 名称，供 SQL 侧按同一时区换算日期，
// 使按日聚合不依赖数据库会话时区（生产常见 PostgreSQL 用 UTC、应用用本地时区）。
// 解析顺序：TZ 环境变量 → time.Local 名称 → /etc/localtime 符号链接 → /etc/timezone → UTC。
//
// 仅凭 time.Local.String() 不够：当依赖 /etc/localtime 而未显式设置 TZ 时，
// 它返回合成名 "Local"，PostgreSQL 无法当作时区解析。后续两步直接读取系统配置，
// 覆盖 systemd（/etc/localtime 符号链接）与 Debian 系（/etc/timezone 文本）两种约定。
func LocalZoneName() string {
	if name := validZone(os.Getenv("TZ")); name != "" {
		return name
	}
	if name := validZone(time.Local.String()); name != "" {
		return name
	}
	if name := zoneFromLocaltimeSymlink(); name != "" {
		return name
	}
	if name := zoneFromEtcTimezone(); name != "" {
		return name
	}
	return "UTC"
}

// validZone 过滤空值与 Go 的合成名 "Local"——它们无法被 PostgreSQL 当作时区解析。
func validZone(name string) string {
	if name = strings.TrimSpace(name); name == "" || name == "Local" {
		return ""
	}
	return name
}

// zoneFromLocaltimeSymlink 解析 /etc/localtime 指向的 zoneinfo 路径，
// 如 /usr/share/zoneinfo/Asia/Shanghai → Asia/Shanghai。systemd 约定。
func zoneFromLocaltimeSymlink() string {
	target, err := filepath.EvalSymlinks("/etc/localtime")
	if err != nil {
		return ""
	}
	const marker = "zoneinfo/"
	if i := strings.Index(target, marker); i >= 0 {
		return validZone(target[i+len(marker):])
	}
	return ""
}

// zoneFromEtcTimezone 读取 Debian 系的 /etc/timezone 文本（单行 IANA 名称）。
func zoneFromEtcTimezone() string {
	data, err := os.ReadFile("/etc/timezone")
	if err != nil {
		return ""
	}
	return validZone(string(data))
}
