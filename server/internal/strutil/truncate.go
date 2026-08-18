// Package strutil 存放跨模块共用的字符串处理，目前只有落库前的长度收敛。
package strutil

import (
	"strings"
	"unicode/utf8"
)

// Truncate 把字符串收敛到至多 maxBytes 字节，并保证结果是合法 UTF-8。
//
// 落库前的截断必须同时满足这两点：数据库列有字节长度上限，而 PostgreSQL 对非法
// UTF-8 直接拒绝整条写入。按字节硬切会把多字节字符切成半个——中文一字三字节，
// 截断点落在字符中间的概率约三分之二——那条记录随即写不进去。上游返回的错误体
// 还可能整体就不是 UTF-8（压缩残片、非 UTF-8 编码的响应），因此除了在字符边界
// 切断，还要清掉剩余的非法字节。
//
// maxBytes 不大于 0 时返回空串。
func Truncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) > maxBytes {
		end := maxBytes
		// 回退到字符起始字节：s[end] 是续字节说明切点落在字符中间。
		for end > 0 && !utf8.RuneStart(s[end]) {
			end--
		}
		s = s[:end]
	}
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "")
}
