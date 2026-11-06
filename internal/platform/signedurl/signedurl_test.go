package signedurl

import (
	"strings"
	"testing"
	"time"
)

func TestFirmaYCaducidad(t *testing.T) {
	s := New("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	tok := s.Token("pdf:c1:r1", now.Add(time.Hour))
	if !s.Valid("pdf:c1:r1", tok, now) {
		t.Fatal("el enlace vigente debe valer")
	}
	if s.Valid("pdf:c1:r2", tok, now) || s.Valid("zip:c1", tok, now) {
		t.Error("el enlace no sirve para otro recurso")
	}
	if s.Valid("pdf:c1:r1", tok, now.Add(2*time.Hour)) {
		t.Error("caducado")
	}
	exp, sig, _ := strings.Cut(tok, ".")
	for _, bad := range []string{"", "x", exp, exp + ".", "9999999999." + sig, exp + "." + sig + "a", "abc." + sig} {
		if s.Valid("pdf:c1:r1", bad, now) {
			t.Errorf("%q no debe valer", bad)
		}
	}
	if New("otro-secreto-distinto-1234").Valid("pdf:c1:r1", tok, now) {
		t.Error("otro secreto no debe validar")
	}
}

func TestSinSecretoNoHayEnlaces(t *testing.T) {
	if New("") != nil || New("corto") != nil {
		t.Error("secreto vacío o corto debe devolver nil")
	}
	var s *Signer
	if s.Valid("x", "1.a", time.Now()) {
		t.Error("un firmador nil nunca valida")
	}
}
