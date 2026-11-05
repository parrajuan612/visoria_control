package main

import "testing"

func TestParseRoles(t *testing.T) {
	r, err := ParseRoles(" api , worker,api ")
	if err != nil || !r.Has(RoleAPI) || !r.Has(RoleWorker) || r.Has(RoleScheduler) {
		t.Fatalf("roles=%v err=%v", r, err)
	}
	if r.String() != "api,worker" {
		t.Errorf("String=%q", r.String())
	}
	all, err := ParseRoles("all")
	if err != nil || len(all) != 3 {
		t.Fatalf("all=%v err=%v", all, err)
	}
	def, err := ParseRoles(defaultRoles)
	if err != nil || len(def) != 3 {
		t.Fatalf("default=%v err=%v", def, err)
	}
}

func TestParseRolesInvalidos(t *testing.T) {
	for _, in := range []string{"", " , ", "api,cron"} {
		if _, err := ParseRoles(in); err == nil {
			t.Errorf("%q debió fallar", in)
		}
	}
}
