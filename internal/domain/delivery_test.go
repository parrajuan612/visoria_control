package domain

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

func TestDecideTablaDelSDD(t *testing.T) {
	W, E := ChannelWhatsApp, ChannelEmail
	cases := []struct {
		name     string
		ch       Channel
		from, to DeliveryStatus
		src      Source
		want     Outcome
	}{
		// flujo feliz
		{"pending→queued", W, StatusPending, StatusQueued, SourceSystem, OutcomeApplied},
		{"pending→skipped", W, StatusPending, StatusSkipped, SourceSystem, OutcomeApplied},
		{"queued→sending", W, StatusQueued, StatusSending, SourceWorker, OutcomeApplied},
		{"sending→accepted", W, StatusSending, StatusAccepted, SourceWorker, OutcomeApplied},
		{"sending→retry", W, StatusSending, StatusRetryWait, SourceWorker, OutcomeApplied},
		{"retry→sending", W, StatusRetryWait, StatusSending, SourceWorker, OutcomeApplied},
		{"retry→failed", W, StatusRetryWait, StatusFailed, SourceWorker, OutcomeApplied},
		{"sending→failed", W, StatusSending, StatusFailed, SourceWorker, OutcomeApplied},
		{"sending→needs_review", W, StatusSending, StatusNeedsReview, SourceSystem, OutcomeApplied},
		// confirmaciones del proveedor
		{"accepted→sent", W, StatusAccepted, StatusSent, SourceWebhook, OutcomeApplied},
		{"sent→delivered", W, StatusSent, StatusDelivered, SourceWebhook, OutcomeApplied},
		{"accepted→delivered (salta sent)", W, StatusAccepted, StatusDelivered, SourceWebhook, OutcomeApplied},
		{"delivered→read", W, StatusDelivered, StatusRead, SourceWebhook, OutcomeApplied},
		{"accepted→read (salta)", W, StatusAccepted, StatusRead, SourceWebhook, OutcomeApplied},
		{"accepted→failed (webhook)", W, StatusAccepted, StatusFailed, SourceWebhook, OutcomeApplied},
		{"sent→failed", W, StatusSent, StatusFailed, SourceWebhook, OutcomeApplied},
		{"accepted→unconfirmed", W, StatusAccepted, StatusUnconfirmed, SourceSystem, OutcomeApplied},
		{"unconfirmed→delivered tardío", W, StatusUnconfirmed, StatusDelivered, SourceWebhook, OutcomeApplied},
		// correo: DSN
		{"email accepted→failed (imap)", E, StatusAccepted, StatusFailed, SourceIMAP, OutcomeApplied},
		// desordenados / duplicados → Ignored
		{"duplicado", W, StatusDelivered, StatusDelivered, SourceWebhook, OutcomeIgnored},
		{"sent tras delivered", W, StatusDelivered, StatusSent, SourceWebhook, OutcomeIgnored},
		{"delivered tras read", W, StatusRead, StatusDelivered, SourceWebhook, OutcomeIgnored},
		{"accepted tras delivered", W, StatusDelivered, StatusAccepted, SourceWorker, OutcomeIgnored},
		{"delivered tras failed (webhook)", W, StatusFailed, StatusDelivered, SourceWebhook, OutcomeIgnored},
		{"failed tras delivered (webhook)", W, StatusDelivered, StatusFailed, SourceWebhook, OutcomeIgnored},
		{"failed tras read (webhook)", W, StatusRead, StatusFailed, SourceWebhook, OutcomeIgnored},
		{"webhook sobre queued", W, StatusQueued, StatusDelivered, SourceWebhook, OutcomeIgnored},
		// ilegales → Rejected
		{"salto queued→accepted", W, StatusQueued, StatusAccepted, SourceWorker, OutcomeRejected},
		{"pending→sending", W, StatusPending, StatusSending, SourceWorker, OutcomeRejected},
		{"failed→delivered por worker", W, StatusFailed, StatusDelivered, SourceWorker, OutcomeRejected},
		{"read→failed por worker", W, StatusRead, StatusFailed, SourceWorker, OutcomeRejected},
		// correo nunca llega a sent/delivered/read/unconfirmed
		{"email→sent", E, StatusAccepted, StatusSent, SourceWebhook, OutcomeRejected},
		{"email→delivered", E, StatusAccepted, StatusDelivered, SourceWebhook, OutcomeRejected},
		{"email→read", E, StatusAccepted, StatusRead, SourceWebhook, OutcomeRejected},
		{"email→unconfirmed", E, StatusAccepted, StatusUnconfirmed, SourceSystem, OutcomeRejected},
		// reencolar solo el operador
		{"failed→queued operador", W, StatusFailed, StatusQueued, SourceOperator, OutcomeApplied},
		{"needs_review→queued operador", W, StatusNeedsReview, StatusQueued, SourceOperator, OutcomeApplied},
		{"unconfirmed→queued operador", W, StatusUnconfirmed, StatusQueued, SourceOperator, OutcomeApplied},
		{"skipped→queued operador", E, StatusSkipped, StatusQueued, SourceOperator, OutcomeApplied},
		{"failed→queued worker", W, StatusFailed, StatusQueued, SourceWorker, OutcomeRejected},
		{"needs_review→queued sistema", W, StatusNeedsReview, StatusQueued, SourceSystem, OutcomeRejected},
		{"skipped→queued sistema", W, StatusSkipped, StatusQueued, SourceSystem, OutcomeRejected},
	}
	for _, c := range cases {
		if got := Decide(c.ch, c.from, c.to, c.src); got != c.want {
			t.Errorf("%s: Decide(%s,%s→%s,%s)=%s, quería %s", c.name, c.ch, c.from, c.to, c.src, got, c.want)
		}
	}
}

// Sea cual sea el orden de llegada de SENT/DELIVERED/READ, el resultado es READ.
func TestEventosDesordenadosConvergen(t *testing.T) {
	events := []DeliveryStatus{StatusSent, StatusDelivered, StatusRead}
	perms := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for _, p := range perms {
		d := NewDelivery("c", "r", ChannelWhatsApp, KindProposal, t0)
		d.Status = StatusAccepted
		for _, i := range p {
			d.Apply(Transition{Key: d.Key, To: events[i], Source: SourceWebhook, At: t0})
		}
		if d.Status != StatusRead {
			t.Errorf("orden %v terminó en %s, quería READ", p, d.Status)
		}
	}
}

// Propiedad: con eventos aleatorios del proveedor el rango nunca baja y FAILED es absorbente.
func TestPropiedadMonotonia(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	pool := []DeliveryStatus{StatusAccepted, StatusSent, StatusDelivered, StatusRead, StatusFailed, StatusUnconfirmed}
	for i := 0; i < 2000; i++ {
		d := NewDelivery("c", "r", ChannelWhatsApp, KindProposal, t0)
		d.Status = StatusAccepted
		maxRank := rank[StatusAccepted]
		failed := false
		for j := 0; j < 12; j++ {
			to := pool[rng.Intn(len(pool))]
			d.Apply(Transition{Key: d.Key, To: to, Source: SourceWebhook, At: t0})
			if d.Status == StatusFailed {
				failed = true
			}
			if failed && d.Status != StatusFailed {
				t.Fatalf("FAILED no es absorbente: terminó en %s", d.Status)
			}
			if r, ok := rank[d.Status]; ok {
				if r < maxRank {
					t.Fatalf("el rango bajó a %s", d.Status)
				}
				maxRank = r
			}
		}
	}
}

func TestApplyIdempotenteYRegistraHistorial(t *testing.T) {
	d := NewDelivery("c1", "r1", ChannelWhatsApp, KindProposal, t0)
	if d.Key != "c1:r1:whatsapp:proposal" || d.Status != StatusPending || len(d.History) != 1 {
		t.Fatalf("NewDelivery: %+v", d)
	}
	step := func(to DeliveryStatus, src Source, mut func(*Transition)) (Outcome, DeliveryStatus) {
		tr := Transition{Key: d.Key, To: to, Source: src, At: t0.Add(time.Minute), Detail: "x"}
		if mut != nil {
			mut(&tr)
		}
		return d.Apply(tr)
	}
	step(StatusQueued, SourceSystem, nil)
	step(StatusSending, SourceWorker, nil)
	step(StatusSending, SourceWorker, nil) // duplicado
	out, prev := step(StatusAccepted, SourceWorker, func(tr *Transition) { tr.ProviderID = "wamid.1" })
	if out != OutcomeApplied || prev != StatusSending || d.ProviderID != "wamid.1" || d.Attempts != 1 {
		t.Fatalf("accepted: out=%s prev=%s d=%+v", out, prev, d)
	}
	if out, _ := step(StatusAccepted, SourceWorker, nil); out != OutcomeIgnored {
		t.Errorf("repetir ACCEPTED debe ignorarse, fue %s", out)
	}
	if out, _ := step(StatusFailed, SourceWebhook, func(tr *Transition) {
		tr.Error = &ProviderError{Code: "131026", Message: "sin WhatsApp", Permanent: true}
	}); out != OutcomeApplied || d.LastError == nil || d.LastError.Code != "131026" {
		t.Fatalf("failed: %s %+v", out, d.LastError)
	}
	// Un evento ilegal no cambia nada.
	hist := len(d.History)
	if out, _ := step(StatusSending, SourceWorker, nil); out != OutcomeRejected || len(d.History) != hist || d.Status != StatusFailed {
		t.Errorf("rechazada debe dejar todo igual: %s %s", out, d.Status)
	}
	// El operador reenvía: se limpia el último error y el historial conserva todo.
	step(StatusQueued, SourceOperator, nil)
	if d.LastError != nil || d.Status != StatusQueued || len(d.History) != hist+1 {
		t.Errorf("requeue: %+v", d)
	}
	step(StatusSending, SourceWorker, nil)
	if d.Attempts != 2 {
		t.Errorf("attempts=%d, quería 2", d.Attempts)
	}
}

func TestValidate(t *testing.T) {
	ok := Transition{Key: "k", To: StatusQueued, Source: SourceSystem}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Transition{
		{Key: " ", To: StatusQueued, Source: SourceSystem},
		{Key: "k", To: "NADA", Source: SourceSystem},
		{Key: "k", To: StatusQueued, Source: "otro"},
	}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("%+v debió fallar", b)
		}
	}
}

func TestIsFinal(t *testing.T) {
	for s, want := range map[DeliveryStatus]bool{
		StatusFailed: true, StatusRead: true, StatusSkipped: true,
		StatusQueued: false, StatusAccepted: false, StatusDelivered: false,
	} {
		if IsFinal(s) != want {
			t.Errorf("IsFinal(%s)", s)
		}
	}
}

func TestRulesSerializa(t *testing.T) {
	raw, err := Rules()
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Rank     map[string]int      `json:"rank"`
		Allowed  map[string][]string `json:"allowed"`
		Operator []string            `json:"operator_requeue_from"`
		Email    []string            `json:"email_forbidden"`
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	if r.Rank["READ"] != 4 || len(r.Allowed["SENDING"]) != 4 || len(r.Operator) != 4 || len(r.Email) != 4 {
		t.Errorf("reglas incompletas: %+v", r)
	}
}

// Todo estado debe poder alcanzarse o ser inicial (la tabla no deja estados huérfanos).
func TestTodosLosEstadosSonAlcanzables(t *testing.T) {
	reach := map[DeliveryStatus]bool{StatusPending: true}
	for changed := true; changed; {
		changed = false
		for _, from := range AllStatuses {
			if !reach[from] {
				continue
			}
			for _, to := range AllStatuses {
				if !reach[to] && Decide(ChannelWhatsApp, from, to, SourceOperator) == OutcomeApplied {
					reach[to], changed = true, true
				}
			}
		}
	}
	for _, s := range AllStatuses {
		if !reach[s] {
			t.Errorf("%s no es alcanzable desde PENDING", s)
		}
	}
}
