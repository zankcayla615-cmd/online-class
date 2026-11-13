// Package clock 提供可注入的业务时钟。
//
// 业务代码一律通过 Clock.Now() 取当前时间，禁止直接调用 time.Now()：
// 截止判定、发奖时间与事件时间必须同源，测试与教学演示才能复现。
package clock

import "time"

// Clock 是带固定偏移的业务时钟，并发安全（只读字段）。
type Clock struct {
	offset time.Duration
}

// New 创建时钟。offset 为教学演示偏移，0 表示与系统时间一致。
func New(offset time.Duration) *Clock { return &Clock{offset: offset} }

// Now 返回 UTC 时间。存储与判定统一使用 UTC，展示层再转换时区。
func (c *Clock) Now() time.Time { return time.Now().UTC().Add(c.offset) }

// Offset 返回当前偏移量，用于页面与日志标注"使用了教学时钟"。
func (c *Clock) Offset() time.Duration { return c.offset }

// At 返回相对现在偏移 d 的时间，便于计算"截止时间"等派生值。
func (c *Clock) At(d time.Duration) time.Time { return c.Now().Add(d) }
