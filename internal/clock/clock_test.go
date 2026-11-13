package clock_test

import (
	"testing"
	"time"

	"onlineclass/internal/clock"
)

func TestNowAppliesOffset(t *testing.T) {
	clk := clock.New(3 * time.Hour)
	diff := clk.Now().Sub(time.Now().UTC())
	if diff < 2*time.Hour+59*time.Minute || diff > 3*time.Hour+time.Minute {
		t.Fatalf("偏移未生效，差值 %v", diff)
	}
	if clk.Offset() != 3*time.Hour {
		t.Fatalf("Offset() 返回值错误: %v", clk.Offset())
	}
}

func TestNowIsUTC(t *testing.T) {
	clk := clock.New(0)
	if loc := clk.Now().Location(); loc != time.UTC {
		t.Fatalf("时间应为 UTC，实际 %v", loc)
	}
}

func TestAt(t *testing.T) {
	clk := clock.New(0)
	got := clk.At(90 * time.Minute)
	if d := time.Until(got); d < 89*time.Minute || d > 91*time.Minute {
		t.Fatalf("At 计算错误: %v", d)
	}
}
