package ports

import (
	"context"
	"testing"
)

func TestAttemptEnContexto(t *testing.T) {
	if a := AttemptFrom(context.Background()); a != (Attempt{}) || a.IsLast() {
		t.Errorf("sin intento: %+v", a)
	}
	for _, c := range []struct {
		a    Attempt
		want bool
	}{{Attempt{0, 5}, false}, {Attempt{4, 5}, false}, {Attempt{5, 5}, true}, {Attempt{6, 5}, true}, {Attempt{0, 0}, false}} {
		if got := AttemptFrom(WithAttempt(context.Background(), c.a)).IsLast(); got != c.want {
			t.Errorf("%+v: %v", c.a, got)
		}
	}
	e := &ProviderError{Action: ActionFail, Code: "131026", Message: "sin WhatsApp"}
	if !e.Permanent() || e.Error() != "sin WhatsApp (131026)" || (&ProviderError{Action: ActionRetry}).Permanent() {
		t.Error("ProviderError")
	}
}
