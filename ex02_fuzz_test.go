package main

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// go test -fuzz FuzzEx02 -fuzztime 15s
func FuzzEx02(f *testing.F) {
	if testing.Short() {
		f.SkipNow()
	}

	const fileName = "examples/ex02.asm"
	if _, err := os.Stat(fileName); err != nil {
		f.Skip(err)
	}

	cases := []uint32{
		0,
		1,
		2,
		3,
		math.MaxUint16 - 1,
		math.MaxUint16,
		math.MaxUint16 + 1,
		math.MaxUint16 + 2,
		math.MaxInt32 - 1,
		math.MaxInt32,
		math.MaxInt32 + 1,
		math.MaxInt32 + 2,
		math.MaxUint32 - 1,
		math.MaxUint32,
	}

	for i, t0 := range cases {
		f.Add(t0, t0)
		for _, t1 := range cases[i+1:] {
			f.Add(t0, t1)
			f.Add(t1, t0)
		}
	}

	f.Fuzz(func(t *testing.T, t0, t1 uint32) {
		handler := NewSimulatorHandler(fileName, entryPoint)
		handler.init("FuzzEx02")
		sim := handler.sharedSimulator()
		if sim.end == nil {
			t.SkipNow()
		}

		sim.instructions[0].Operand = "x0,x0,0" // li -> nop
		sim.instructions[1].Operand = "x0,x0,0" // li -> nop

		sim.registers[5] = t0
		sim.registers[6] = t1

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/", strings.NewReader("button=RUN"))
		handler.ServeHTTP(w, r)

		if w.Code != http.StatusOK {
			t.Fatalf("Code = %d", w.Code)
		}

		m := sim.memory[0]
		if load32(m, 0) != t0 || load32(m, 4) != t1 {
			t.Fatalf("t0 = %d t1 = %d want %d %d", load32(m, 0), load32(m, 4), t0, t1)
		}

		sum := uint64(t0) + uint64(t1)
		got64 := load64(m, 0x10)
		if got64 != sum {
			t.Errorf("t0 = %d t1 = %d addu = %d, want %d", t0, t1, got64, sum)
		}

		difference := t0 - t1
		borrow := uint32(0)
		if t0 < t1 {
			borrow = 1
		}
		got := load32(m, 0x20)
		if got != difference {
			t.Errorf("t0 = %d t1 = %d subu = %d, want %d", t0, t1, got, difference)
		}
		got = load32(m, 0x24)
		if got != borrow {
			t.Errorf("t0 = %d t1 = %d borrow = %d, want %d", t0, t1, got, borrow)
		}

		product := uint64(t0) * uint64(t1)
		got64 = load64(m, 0x30)
		if got64 != product {
			t.Errorf("t0 = %d t1 = %d mulu = %d, want %d", t0, t1, got64, product)
		}

		quotient, remainder := uint32(0xffffffff), uint32(t0)
		if t1 != 0 {
			quotient = t0 / t1
			remainder = t0 % t1
		}
		got = load32(m, 0x40)
		if got != quotient {
			t.Errorf("t0 = %d t1 = %d divu = %d, want %d", t0, t1, got, quotient)
		}
		got = load32(m, 0x44)
		if got != remainder {
			t.Errorf("t0 = %d t1 = %d mod = %d, want %d", t0, t1, got, remainder)
		}
	})
}

func load32(m []byte, addr int) uint32 {
	return uint32(m[addr]) |
		(uint32(m[addr+1]) << 8) |
		(uint32(m[addr+2]) << 16) |
		(uint32(m[addr+3]) << 24)
}

func load64(m []byte, addr int) uint64 {
	return uint64(m[addr]) |
		(uint64(m[addr+1]) << 8) |
		(uint64(m[addr+2]) << 16) |
		(uint64(m[addr+4]) << 32) |
		(uint64(m[addr+3]) << 24) |
		(uint64(m[addr+5]) << 40) |
		(uint64(m[addr+6]) << 48) |
		(uint64(m[addr+7]) << 56)
}
