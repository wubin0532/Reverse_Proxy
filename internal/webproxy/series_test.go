package webproxy

import (
	"testing"
	"time"
)

// TestSiteSeriesBuffer 分钟桶聚合、稠密补零与容量上限。
func TestSiteSeriesBuffer(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).Truncate(time.Minute)
	var s siteSeries
	s.add(base, 10, 20)
	s.add(base.Add(30*time.Second), 5, 5) // 同一分钟
	s.add(base.Add(2*time.Minute), 1, 2)  // 跳一分钟
	s.add(base.Add(-time.Minute), 100, 0) // 旧分钟（迟到）

	m0 := base.Unix() / 60
	points := s.snapshot(m0-1, m0+2)
	if len(points) != 4 {
		t.Fatalf("稠密序列应含 4 个点, got %d", len(points))
	}
	if points[0].requests != 1 || points[0].bytesIn != 100 {
		t.Fatalf("迟到分钟应计入: %+v", points[0])
	}
	if points[1].requests != 2 || points[1].bytesIn != 15 || points[1].bytesOut != 25 {
		t.Fatalf("同分钟应聚合: %+v", points[1])
	}
	if points[2].requests != 0 {
		t.Fatalf("无流量分钟应补零: %+v", points[2])
	}
	if points[3].requests != 1 {
		t.Fatalf("第三分钟应有 1 次请求: %+v", points[3])
	}

	// 容量上限：完成桶最多 1440 个，最旧的丢弃
	var big siteSeries
	for i := 0; i < seriesMaxPoints+10; i++ {
		big.add(base.Add(time.Duration(i)*time.Minute), 1, 1)
	}
	if len(big.points) != seriesMaxPoints {
		t.Fatalf("完成桶应封顶 %d, got %d", seriesMaxPoints, len(big.points))
	}
	if big.points[0].minute != m0+9 {
		t.Fatalf("应丢弃最旧桶, 首桶分钟 = %d", big.points[0].minute)
	}
}

// TestSiteSeriesAPIView Service 层序列视图：实际请求计入当前分钟，历史补零。
func TestSiteSeriesAPIView(t *testing.T) {
	cfg, svc := newTestService(t)
	_ = cfg
	svc.statsFor("s1").finish("r1", 200, 11, 22)

	view := svc.SiteSeries("s1", time.Hour)
	if view.Step != 60 || len(view.Points) != 60 {
		t.Fatalf("1h 序列应为 60 个点, got %d", len(view.Points))
	}
	last := view.Points[len(view.Points)-1]
	if last[1] != 1 || last[2] != 11 || last[3] != 22 {
		t.Fatalf("当前分钟应含 1 次请求: %v", last)
	}
	if view.Points[0][1] != 0 {
		t.Fatalf("历史分钟应补零: %v", view.Points[0])
	}
	if view.Points[0][0] != view.From || view.Points[len(view.Points)-1][0] != view.To {
		t.Fatal("点时间戳应与 from/to 对齐")
	}

	// 无统计数据的站点返回零填充序列
	empty := svc.SiteSeries("ghost", 6*time.Hour)
	if len(empty.Points) != 360 {
		t.Fatalf("6h 序列应为 360 个点, got %d", len(empty.Points))
	}
	for _, p := range empty.Points {
		if p[1] != 0 || p[2] != 0 || p[3] != 0 {
			t.Fatalf("未知站点应全零: %v", p)
		}
	}
}
