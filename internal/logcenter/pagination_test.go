package logcenter

import (
	"fmt"
	"testing"
	"time"
)

// 翻页期间有新日志写入时游标必须稳定：原实现用结果集下标，新条目会让下标
// 整体位移，导致第 2 页重复第 1 页尾部的若干条。
func TestQueryCursorStableAcrossNewEntries(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	base := time.Now()
	for i := 0; i < 6; i++ {
		c.Add(Entry{Time: base.Add(time.Duration(i) * time.Millisecond), Level: "info", Source: "webproxy", Message: fmt.Sprintf("req-%d", i)})
	}
	page1, next := c.Query(Query{Limit: 3})
	if len(page1) != 3 {
		t.Fatalf("第 1 页应有 3 条, got %d", len(page1))
	}
	if page1[0].Message != "req-5" || page1[2].Message != "req-3" {
		t.Fatalf("第 1 页顺序异常: %s..%s", page1[0].Message, page1[2].Message)
	}
	if next <= 0 {
		t.Fatalf("第 1 页应返回正数游标, got %d", next)
	}

	// 翻页之间写入新日志。
	c.Add(Entry{Time: base.Add(time.Hour), Level: "info", Source: "webproxy", Message: "req-new"})

	page2, next2 := c.Query(Query{Limit: 3, Cursor: next})
	if len(page2) != 3 {
		t.Fatalf("第 2 页应有 3 条, got %d: %+v", len(page2), page2)
	}
	seen := map[string]bool{}
	for _, e := range page1 {
		seen[e.Message] = true
	}
	for _, e := range page2 {
		if seen[e.Message] {
			t.Fatalf("第 2 页重复了第 1 页的条目: %s", e.Message)
		}
	}
	if page2[0].Message != "req-2" || page2[2].Message != "req-0" {
		t.Fatalf("第 2 页内容异常: %s..%s", page2[0].Message, page2[2].Message)
	}

	// 第 2 页已取完全部剩余条目，必须返回 -1（前端据此禁用"加载更多"）。
	if next2 != -1 {
		t.Fatalf("已取完时 nextCursor 应为 -1, got %d", next2)
	}
}

// 游标指向已被挤出的条目时从头开始（宁可重复不可漏）。
func TestQueryCursorFallsBackWhenEntryEvicted(t *testing.T) {
	c, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	c.Add(Entry{Time: time.Now(), Level: "info", Source: "webproxy", Message: "only"})
	page, next := c.Query(Query{Limit: 10, Cursor: 999999})
	if len(page) != 1 || page[0].Message != "only" {
		t.Fatalf("未知游标应从头返回, got %+v", page)
	}
	if next != -1 {
		t.Fatalf("单条记录后不应有下一页, got %d", next)
	}
}
