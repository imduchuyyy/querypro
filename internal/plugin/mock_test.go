package plugin

import (
	"context"
	"testing"
	"time"
)

func TestMockActionsRun(t *testing.T) {
	latency = func() time.Duration { return 0 }
	for kind := range mocks {
		p := Mock(kind)
		ctx, cancel := context.WithCancel(context.Background())
		if _, err := p.Query(ctx, p.Placeholder()); err != nil {
			t.Errorf("%s placeholder %q: %v", kind, p.Placeholder(), err)
		}
		for _, r := range p.Resources() {
			for _, a := range p.Actions(r) {
				if _, err := p.Query(ctx, a.Query); err != nil {
					t.Errorf("%s %s %q: %v", kind, r.Name, a.Query, err)
				}
			}
		}
		cancel()
	}
}
