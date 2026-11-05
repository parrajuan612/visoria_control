// Package httpx ofrece clientes HTTP con timeouts y un Transport compartido
// (reutiliza conexiones). Nunca usar http.DefaultClient ni http.Get.
package httpx

import (
	"net"
	"net/http"
	"time"
)

const (
	ShortTimeout = 10 * time.Second // APIs internas (Chatwoot)
	LongTimeout  = 30 * time.Second // APIs externas (Meta, Google Sheets)
)

var transport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: 25 * time.Second,
	MaxIdleConns:          50,
	MaxIdleConnsPerHost:   10,
	IdleConnTimeout:       90 * time.Second,
}

// New devuelve un cliente con el timeout total indicado que comparte el Transport.
func New(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: transport}
}
