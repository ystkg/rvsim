package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestEx01(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	const fileName = "examples/ex01.asm"
	if _, err := os.Stat(fileName); err != nil {
		t.Skip(err)
	}

	handler := NewSimulatorHandler(fileName, entryPoint)
	handler.init("TestEx01")
	sim := handler.sharedSimulator()
	if sim.end == nil {
		t.SkipNow()
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/", strings.NewReader("button=RUN"))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d", w.Code)
	}

	m := sim.memory[0]
	got := string([]byte{m[0], m[1], m[2], m[3], m[4], m[5]})
	want := "RISC-V"
	if got != want {
		t.Errorf("%s, want %s", got, want)
	}
}
