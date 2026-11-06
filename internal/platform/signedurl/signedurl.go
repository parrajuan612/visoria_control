// Package signedurl firma enlaces con HMAC-SHA256 y caducidad. Permite compartir un recurso concreto
// (un PDF, un ZIP) sin que el destinatario tenga sesión: el enlace SOLO sirve para ese recurso y hasta
// la fecha firmada, y no se puede fabricar sin el secreto.
package signedurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

type Signer struct{ secret []byte }

// MinSecretLen: `openssl rand -hex 32` da 64 caracteres. Un texto de ejemplo («tu_clave_secreta…») es corto y
// adivinable, así que se rechaza en lugar de aceptarlo como si fuera un secreto.
const MinSecretLen = 32

// New devuelve nil si el secreto está vacío o es demasiado corto: sin secreto no hay enlaces.
func New(secret string) *Signer {
	if len(secret) < MinSecretLen {
		return nil
	}
	return &Signer{secret: []byte(secret)}
}

func (s *Signer) mac(resource string, exp int64) string {
	h := hmac.New(sha256.New, s.secret)
	h.Write([]byte(resource))
	h.Write([]byte{0})
	h.Write([]byte(strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}

// Token es «caducidad.firma» para el recurso dado (por ejemplo "pdf:{campaña}:{destinatario}").
func (s *Signer) Token(resource string, exp time.Time) string {
	e := exp.Unix()
	return strconv.FormatInt(e, 10) + "." + s.mac(resource, e)
}

// Valid comprueba la firma (en tiempo constante) y que no haya caducado.
func (s *Signer) Valid(resource, token string, now time.Time) bool {
	if s == nil {
		return false
	}
	expStr, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || now.Unix() > exp {
		return false
	}
	return hmac.Equal([]byte(sig), []byte(s.mac(resource, exp)))
}
