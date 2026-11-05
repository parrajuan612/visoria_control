package main

import (
	"fmt"
	"sort"
	"strings"
)

// Role es una responsabilidad del binario. En el VPS pequeño corren todas en
// el mismo proceso (SDD §3.1); más adelante pueden separarse con --roles.
type Role string

const (
	RoleAPI       Role = "api"       // HTTP, SSE, webhooks
	RoleWorker    Role = "worker"    // consume colas asynq (Fase 1.4+)
	RoleScheduler Role = "scheduler" // tareas periódicas y recordatorios
)

const defaultRoles = "api,worker,scheduler"

// Roles es el conjunto de roles activos.
type Roles map[Role]bool

func (r Roles) Has(role Role) bool { return r[role] }

func (r Roles) String() string {
	names := make([]string, 0, len(r))
	for role := range r {
		names = append(names, string(role))
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// ParseRoles interpreta "api,worker" (ignora espacios y duplicados; "all" = todos).
func ParseRoles(s string) (Roles, error) {
	out := Roles{}
	for _, part := range strings.Split(s, ",") {
		part = strings.ToLower(strings.TrimSpace(part))
		switch Role(part) {
		case "":
		case RoleAPI, RoleWorker, RoleScheduler:
			out[Role(part)] = true
		default:
			if part == "all" {
				out[RoleAPI], out[RoleWorker], out[RoleScheduler] = true, true, true
				continue
			}
			return nil, fmt.Errorf("rol desconocido %q (válidos: api, worker, scheduler, all)", part)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("--roles no puede estar vacío (válidos: api, worker, scheduler, all)")
	}
	return out, nil
}
