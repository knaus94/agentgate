package dev

import (
	"testing"
)

func TestConsultationNamesCannotEscapeOrCollide(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"../outside", "a/b", "/tmp/outside", ".", "..", "", "bad name"} {
		if _, err := Consult(f.cfg, ConsultInput{Lanes: []ConsultLane{{Name: name, Question: "read the plan"}}}); err == nil {
			t.Errorf("unsafe lane accepted: %q", name)
		}
	}
	if _, err := Consult(f.cfg, ConsultInput{Lanes: []ConsultLane{{Name: "same", Question: "first"}, {Name: "same", Question: "second"}}}); err == nil {
		t.Fatal("duplicate lanes share and overwrite their receipt paths")
	}
}
