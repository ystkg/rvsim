package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScenario01(t *testing.T) {
	if testing.Short() {
		t.SkipNow()
	}

	file, err := os.Create(filepath.Join(t.TempDir(), "scenario01.asm"))
	if err != nil {
		t.Skip(err)
	}
	file.Close()

	handler := NewSimulatorHandler(file.Name(), entryPoint)
	handler.init("TestScenario01")
	sim := handler.sharedSimulator()
	sim.validationError = &StringRecorder{[]string{}}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want %d", w.Code, http.StatusOK)
	}

	file, err = os.OpenFile(file.Name(), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Skip(err)
	}
	file.WriteString("addi x0, x0, 0\n") // NOP
	file.WriteString("addi 0, 0, 0\n")   // invalid
	file.Close()

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/", strings.NewReader("button=RELOAD"))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want %d", w.Code, http.StatusOK)
	}

	file, err = os.OpenFile(file.Name(), os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		t.Skip(err)
	}
	file.WriteString("addi x0, x0, 0\n") // NOP
	file.WriteString("addi x0, x0, 0\n") // fixed
	file.Close()

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/", strings.NewReader("button=RELOAD"))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want %d", w.Code, http.StatusOK)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/", strings.NewReader("button=STEP"))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want %d", w.Code, http.StatusOK)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/", strings.NewReader("button=STEP"))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want %d", w.Code, http.StatusOK)
	}

	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/", strings.NewReader("button=STOP"))
	handler.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want %d", w.Code, http.StatusOK)
	}
}
