package domain

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Channel es el canal de una entrega.
type Channel string

const (
	ChannelWhatsApp Channel = "whatsapp"
	ChannelEmail    Channel = "email"
)

// Kind es el tipo de mensaje.
type Kind string

const (
	KindProposal  Kind = "proposal"
	KindReminder1 Kind = "reminder_1"
	KindReminder2 Kind = "reminder_2"
	KindReminder3 Kind = "reminder_3"
)

// DeliveryStatus es el estado de una entrega (SDD §3.5.1).
type DeliveryStatus string

const (
	StatusPending     DeliveryStatus = "PENDING"
	StatusSkipped     DeliveryStatus = "SKIPPED"
	StatusQueued      DeliveryStatus = "QUEUED"
	StatusSending     DeliveryStatus = "SENDING"
	StatusRetryWait   DeliveryStatus = "RETRY_WAIT"
	StatusAccepted    DeliveryStatus = "ACCEPTED"
	StatusSent        DeliveryStatus = "SENT"
	StatusDelivered   DeliveryStatus = "DELIVERED"
	StatusRead        DeliveryStatus = "READ"
	StatusFailed      DeliveryStatus = "FAILED"
	StatusUnconfirmed DeliveryStatus = "UNCONFIRMED"
	StatusNeedsReview DeliveryStatus = "NEEDS_REVIEW"
)

// AllStatuses lista todos los estados (útil para contadores y tests).
var AllStatuses = []DeliveryStatus{
	StatusPending, StatusSkipped, StatusQueued, StatusSending, StatusRetryWait,
	StatusAccepted, StatusSent, StatusDelivered, StatusRead, StatusFailed,
	StatusUnconfirmed, StatusNeedsReview,
}

// Source indica quién originó una transición.
type Source string

const (
	SourceWorker   Source = "worker"
	SourceWebhook  Source = "webhook"
	SourceIMAP     Source = "imap"
	SourceOperator Source = "operator"
	SourceSystem   Source = "system" // barridos periódicos (p. ej. UNCONFIRMED)
)

// Outcome es el resultado de intentar una transición.
type Outcome string

const (
	// OutcomeApplied: el estado cambió.
	OutcomeApplied Outcome = "APPLIED"
	// OutcomeIgnored: evento duplicado, desordenado o tardío; es normal y no cambia nada.
	OutcomeIgnored Outcome = "IGNORED"
	// OutcomeRejected: transición ilegal (error de lógica del llamador); no cambia nada.
	OutcomeRejected Outcome = "REJECTED"
)

// ---------------------------------------------------------------------------
// Reglas. Es la ÚNICA fuente de verdad: Decide las usa en Go y el adaptador de
// Redis las incrusta en el script Lua (ver Rules()).
// ---------------------------------------------------------------------------

// rank ordena los estados que confirma el proveedor (rango monótono):
// ACCEPTED(1) < SENT(2) < DELIVERED(3) < READ(4). Entre ellos solo se sube.
var rank = map[DeliveryStatus]int{
	StatusAccepted: 1, StatusSent: 2, StatusDelivered: 3, StatusRead: 4,
}

// allowed son el resto de transiciones válidas (las de rango se resuelven con rank).
var allowed = map[DeliveryStatus][]DeliveryStatus{
	StatusPending:   {StatusSkipped, StatusQueued},
	StatusQueued:    {StatusSending},
	StatusSending:   {StatusAccepted, StatusRetryWait, StatusFailed, StatusNeedsReview},
	StatusRetryWait: {StatusSending, StatusFailed},
	StatusAccepted:  {StatusFailed, StatusUnconfirmed},
	StatusSent:      {StatusFailed},
	// Una confirmación tardía puede llegar después del barrido de 24 h.
	StatusUnconfirmed: {StatusSent, StatusDelivered, StatusRead, StatusFailed, StatusQueued},
	StatusNeedsReview: {StatusQueued},
	StatusFailed:      {StatusQueued},
	StatusSkipped:     {StatusQueued}, // el operador corrige el dato y envía
	// DELIVERED y READ no tienen salidas fuera del rango; FAILED/DELIVERED/READ son finales.
}

// operatorRequeueFrom: reencolar desde estos estados solo lo decide el operador.
var operatorRequeueFrom = []DeliveryStatus{StatusFailed, StatusNeedsReview, StatusUnconfirmed, StatusSkipped}

// emailForbidden: un correo no puede ser "enviado/entregado/leído/sin confirmar":
// el protocolo no lo confirma. Su máximo es ACCEPTED (SDD §3.5.1).
var emailForbidden = []DeliveryStatus{StatusSent, StatusDelivered, StatusRead, StatusUnconfirmed}

func contains(list []DeliveryStatus, s DeliveryStatus) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Decide aplica las reglas de transición. Es una función pura.
//
//  1. Mismo estado → Ignored (duplicado).
//  2. Un correo no puede alcanzar estados que su protocolo no confirma → Rejected.
//  3. Entre estados con rango, solo se sube; bajar o igualar → Ignored (desordenado).
//  4. Si la transición está en la tabla → Applied. Reencolar desde un estado
//     terminal o incierto exige al operador (si no, Rejected).
//  5. Cualquier otra: si viene de un proveedor (webhook/imap) es un evento
//     tardío o ya superado → Ignored; si viene de nosotros es un error → Rejected.
func Decide(ch Channel, from, to DeliveryStatus, src Source) Outcome {
	if from == to {
		return OutcomeIgnored
	}
	if ch == ChannelEmail && contains(emailForbidden, to) {
		return OutcomeRejected
	}
	rf, okf := rank[from]
	rt, okt := rank[to]
	if okf && okt {
		if rt > rf {
			return OutcomeApplied
		}
		return OutcomeIgnored
	}
	if contains(allowed[from], to) {
		if to == StatusQueued && contains(operatorRequeueFrom, from) && src != SourceOperator {
			return OutcomeRejected
		}
		return OutcomeApplied
	}
	if src == SourceWebhook || src == SourceIMAP {
		return OutcomeIgnored
	}
	return OutcomeRejected
}

// IsFinal indica que el estado no admite más avance automático.
func IsFinal(s DeliveryStatus) bool {
	return s == StatusFailed || s == StatusRead || s == StatusSkipped
}

// Rules serializa las reglas para incrustarlas en el script Lua de Redis.
func Rules() ([]byte, error) {
	toStrings := func(l []DeliveryStatus) []string {
		out := make([]string, len(l))
		for i, s := range l {
			out[i] = string(s)
		}
		return out
	}
	al := map[string][]string{}
	for from, tos := range allowed {
		al[string(from)] = toStrings(tos)
	}
	rk := map[string]int{}
	for s, r := range rank {
		rk[string(s)] = r
	}
	return json.Marshal(map[string]any{
		"rank":                  rk,
		"allowed":               al,
		"operator_requeue_from": toStrings(operatorRequeueFrom),
		"email_forbidden":       toStrings(emailForbidden),
	})
}

// ---------------------------------------------------------------------------
// Entidades
// ---------------------------------------------------------------------------

// ProviderError describe un fallo del proveedor.
type ProviderError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Permanent bool   `json:"permanent"`
	Raw       string `json:"raw,omitempty"`
}

// StatusEvent es una entrada del historial.
type StatusEvent struct {
	Status DeliveryStatus `json:"status"`
	At     time.Time      `json:"at"`
	Source Source         `json:"source"`
	Detail string         `json:"detail,omitempty"`
}

// Delivery es una entrega: destinatario × canal × tipo de mensaje.
type Delivery struct {
	Key          string         `json:"key"` // "{campaign}:{recipient}:{channel}:{kind}"
	Channel      Channel        `json:"channel"`
	Kind         Kind           `json:"kind"`
	Status       DeliveryStatus `json:"status"`
	ProviderID   string         `json:"provider_id,omitempty"` // wamid o Message-ID
	Attempts     int            `json:"attempts"`
	LastError    *ProviderError `json:"last_error,omitempty"`
	ScheduledFor *time.Time     `json:"scheduled_for,omitempty"`
	History      []StatusEvent  `json:"history"`
}

// NewDeliveryKey arma la clave idempotente de una entrega.
func NewDeliveryKey(campaignID, recipientID string, ch Channel, kind Kind) string {
	return fmt.Sprintf("%s:%s:%s:%s", campaignID, recipientID, ch, kind)
}

// ParseDeliveryKey descompone "{campaña}:{destinatario}:{canal}:{tipo}".
func ParseDeliveryKey(key string) (campaignID, recipientID string, ch Channel, kind Kind, err error) {
	p := strings.Split(key, ":")
	if len(p) != 4 || p[0] == "" || p[1] == "" {
		return "", "", "", "", fmt.Errorf("clave de entrega no válida: %q", key)
	}
	ch, kind = Channel(p[2]), Kind(p[3])
	if ch != ChannelWhatsApp && ch != ChannelEmail {
		return "", "", "", "", fmt.Errorf("canal desconocido en la clave: %q", p[2])
	}
	return p[0], p[1], ch, kind, nil
}

// NewDelivery crea una entrega en PENDING con su primer evento de historial.
func NewDelivery(campaignID, recipientID string, ch Channel, kind Kind, at time.Time) Delivery {
	return Delivery{
		Key:     NewDeliveryKey(campaignID, recipientID, ch, kind),
		Channel: ch,
		Kind:    kind,
		Status:  StatusPending,
		History: []StatusEvent{{Status: StatusPending, At: at.UTC(), Source: SourceSystem}},
	}
}

// Transition es una petición de cambio de estado.
type Transition struct {
	Key        string // deliveryKey
	To         DeliveryStatus
	Source     Source
	At         time.Time
	ProviderID string         // wamid / Message-ID (opcional; se indexa para correlacionar)
	Error      *ProviderError // para FAILED / RETRY_WAIT
	Detail     string
}

// Validate comprueba lo mínimo de una petición.
func (t Transition) Validate() error {
	if strings.TrimSpace(t.Key) == "" {
		return fmt.Errorf("transición sin clave de entrega")
	}
	valid := false
	for _, s := range AllStatuses {
		if s == t.To {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("estado destino desconocido %q", t.To)
	}
	switch t.Source {
	case SourceWorker, SourceWebhook, SourceIMAP, SourceOperator, SourceSystem:
	default:
		return fmt.Errorf("origen desconocido %q", t.Source)
	}
	return nil
}

// Apply aplica la transición en memoria con las mismas reglas que el script Lua.
// Devuelve el resultado y el estado previo.
func (d *Delivery) Apply(t Transition) (Outcome, DeliveryStatus) {
	prev := d.Status
	out := Decide(d.Channel, prev, t.To, t.Source)
	if out != OutcomeApplied {
		return out, prev
	}
	d.Status = t.To
	d.History = append(d.History, StatusEvent{Status: t.To, At: t.At.UTC(), Source: t.Source, Detail: t.Detail})
	if t.To == StatusSending {
		d.Attempts++
	}
	if t.ProviderID != "" {
		d.ProviderID = t.ProviderID
	}
	if t.Error != nil {
		e := *t.Error
		d.LastError = &e
	}
	if t.To == StatusQueued {
		d.LastError = nil
	}
	return out, prev
}

// StatusLabel es la etiqueta honesta que ve el operador (SDD §6.2). Un correo nunca figura como
// "Entregado": el protocolo no lo confirma.
func StatusLabel(ch Channel, s DeliveryStatus) string {
	switch s {
	case StatusPending, StatusQueued:
		return "En cola"
	case StatusSending:
		return "Enviando"
	case StatusRetryWait:
		return "Reintentando"
	case StatusAccepted:
		if ch == ChannelEmail {
			return "Aceptado por Gmail"
		}
		return "Aceptado por WhatsApp"
	case StatusSent:
		return "Enviado"
	case StatusDelivered:
		return "Entregado"
	case StatusRead:
		return "Leído"
	case StatusFailed:
		return "No entregado"
	case StatusUnconfirmed:
		return "Sin confirmación"
	case StatusNeedsReview:
		return "Revisar"
	case StatusSkipped:
		return "Omitido"
	}
	return string(s)
}

// IsInFlight indica que la entrega aún no llegó a un estado en el que el sistema termine su trabajo.
func IsInFlight(s DeliveryStatus) bool {
	switch s {
	case StatusPending, StatusQueued, StatusSending, StatusRetryWait:
		return true
	}
	return false
}
