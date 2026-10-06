package cover

import (
	"testing"
)

func resultFrom(t *testing.T, runs map[string]string) *Result {
	t.Helper()
	m := NewMerger()
	for id, body := range runs {
		if _, err := m.Import(prof(t, id, body)); err != nil {
			t.Fatal(err)
		}
	}
	return m.Result(nil)
}

func TestThresholdPassAndFail(t *testing.T) {
	res := resultFrom(t, map[string]string{"r": `mode: count
pkg/a.go:1.1,2.1 1 1
pkg/a.go:3.1,4.1 1 0
pkg/sub/b.go:1.1,2.1 1 1
`})
	// overall 2/3 = 66.67; pkg = 50; pkg/sub = 100
	if ge := Check(res, nil, Thresholds{Overall: 60}); ge != nil {
		t.Fatalf("want pass, got %v", ge)
	}
	ge := Check(res, nil, Thresholds{Overall: 70, Packages: map[string]float64{"pkg": 80, "pkg/sub": 100}})
	if ge == nil {
		t.Fatal("want gate error")
	}
	var scopes []string
	for _, v := range ge.Violations {
		scopes = append(scopes, v.Scope)
	}
	// overall + package:pkg fail; package:pkg/sub passes.
	want := map[string]bool{"overall": true, "package:pkg": true}
	for _, s := range scopes {
		if !want[s] {
			t.Fatalf("unexpected violation scope %q", s)
		}
		delete(want, s)
	}
	if len(want) != 0 {
		t.Fatalf("missing violations: %v", want)
	}
	if len(ge.Worst) == 0 || ge.Worst[0].Path != "pkg/a.go" {
		t.Fatalf("worst = %+v", ge.Worst)
	}
	if ge.Worst[0].Uncovered != 1 {
		t.Fatalf("worst uncovered = %d", ge.Worst[0].Uncovered)
	}
}

func TestThresholdDeterministic(t *testing.T) {
	runs := map[string]string{"r": `mode: count
a/x.go:1.1,2.1 1 0
a/y.go:1.1,2.1 1 0
b/z.go:1.1,2.1 1 1
`}
	res := resultFrom(t, runs)
	g1 := Check(res, nil, Thresholds{Overall: 90, TopN: 2})
	g2 := Check(res, nil, Thresholds{Overall: 90, TopN: 2})
	if len(g1.Worst) != 2 || len(g2.Worst) != 2 {
		t.Fatal("TopN not applied")
	}
	for i := range g1.Worst {
		if g1.Worst[i] != g2.Worst[i] {
			t.Fatal("non-deterministic worst ordering")
		}
	}
}

func TestThresholdRegressionDelta(t *testing.T) {
	base := resultFrom(t, map[string]string{"r": "mode: count\npkg/a.go:1.1,2.1 1 1\n"})
	cur := resultFrom(t, map[string]string{"r": "mode: count\npkg/a.go:1.1,2.1 1 0\n"})
	ge := Check(cur, base, Thresholds{Overall: 0})
	if ge != nil {
		t.Fatalf("no violations expected, got %v", ge)
	}
	// With a violation, worst shows the regression delta.
	ge = Check(cur, base, Thresholds{Overall: 50})
	if ge == nil || len(ge.Worst) != 1 {
		t.Fatalf("want violation with worst, got %+v", ge)
	}
	if ge.Worst[0].Delta != -100 {
		t.Fatalf("delta = %v, want -100", ge.Worst[0].Delta)
	}
}
