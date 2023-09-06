package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

type StringRecorder struct {
	messages []string
}

func (r *StringRecorder) Write(p []byte) (n int, err error) {
	r.messages = append(r.messages, string(p))
	return len(p), nil
}

func newTestSimulatorHandler() (*SimulatorHandler, *Simulator) {
	handler := NewSimulatorHandler("", entryPoint)
	sim := NewSimulator(handler.fileName, handler.entryPoint, handler.singlePage, &StringRecorder{[]string{}})
	handler.sims[handler.sharedId] = sim
	return handler, sim
}

func newRequest(body string) *http.Request {
	return httptest.NewRequest("POST", "/", strings.NewReader(body))
}

func TestInstructionRegister(t *testing.T) {
	handler, sim := newTestSimulatorHandler()

	var x [32]uint32
	for i := range x {
		x[i] = uint32(i*100 + i)
	}
	x[28] = 3
	x[29] = 0x0100
	x[30] = 0xfffffff0
	x[31] = 0xa0002001

	m := make([]byte, 16*16)
	m[0x10] = 0x11
	m[0x20], m[0x21] = 0x2468&0xff, 0x2468>>8
	m[0x40], m[0x41], m[0x42], m[0x43] = 0x12345678&0xff, (0x12345678>>8)&0xff, (0x12345678>>16)&0xff, 0x12345678>>24
	m[0xa0] = 0x81                            // sign bit = 1
	m[0xb0], m[0xb1] = 0x8486&0xff, 0x8486>>8 // sign bit = 1

	cases := []struct {
		mnemonic string
		operand  string
		want     uint32
	}{
		{"add  ", "x7, x5, x6 ", x[5] + x[6]},
		{"addi ", "x7, x5, 506", x[5] + 506},
		{"addi ", "x7, x5, -506", x[5] - 506},
		{"addi ", "x0, x5, 506", 0},
		{"sub  ", "x7, x5, x6 ", x[5] - x[6]},
		{"and  ", "x7, x5, x6 ", x[5] & x[6]},
		{"andi ", "x7, x5, 506", x[5] & 506},
		{"or   ", "x7, x5, x6 ", x[5] | x[6]},
		{"ori  ", "x7, x5, 506", x[5] | 506},
		{"xori ", "x7, x5, 506", x[5] ^ 506},
		{"xor  ", "x7, x5, x6 ", x[5] ^ x[6]},
		{"sll  ", "x7, x5, x28", x[5] << x[28]},
		{"slli ", "x7, x5, 6  ", x[5] << 6},
		{"srl  ", "x7, x5, x28", x[5] >> x[28]},
		{"srl  ", "x7, x31, x28", x[31] >> x[28]},
		{"srli ", "x7, x5, 2  ", x[5] >> 2},
		{"srli ", "x7, x31, 2  ", x[31] >> 2},
		{"sra  ", "x7, x5, x28", uint32(int32(x[5]) >> int32(x[28]))},
		{"sra  ", "x7, x31, x28", uint32(int32(x[31]) >> int32(x[28]))},
		{"srai ", "x7, x5, 2  ", uint32(int32(x[5]) >> 2)},
		{"srai ", "x7, x31, 2  ", uint32(int32(x[31]) >> 2)},
		{"slt  ", "x7, x5, x6 ", 1},
		{"slt  ", "x7, x5, x5 ", 0},
		{"slt  ", "x7, x6, x5 ", 0},
		{"slt  ", "x7, x29, x30 ", 0},
		{"slt  ", "x7, x30, x29 ", 1},
		{"slt  ", "x7, x30, x31 ", 0},
		{"slt  ", "x7, x31, x30 ", 1},
		{"sltu ", "x7, x5, x6 ", 1},
		{"sltu ", "x7, x5, x5 ", 0},
		{"sltu ", "x7, x6, x5 ", 0},
		{"sltu ", "x7, x29, x30 ", 1},
		{"sltu ", "x7, x30, x29 ", 0},
		{"sltu ", "x7, x30, x31 ", 0},
		{"sltu ", "x7, x31, x30 ", 1},
		{"slti ", "x7, x5, 506", 1},
		{"slti ", "x7, x5, 505", 0},
		{"slti ", "x7, x5, 504", 0},
		{"sltiu", "x7, x5, 506", 1},
		{"sltiu", "x7, x5, 505", 0},
		{"sltiu", "x7, x5, 504", 0},
		{"lui  ", "x7, 0x12345", 0x12345000},
		{"auipc", "x7, 0x12345", 0x12346000}, // pc - 0x1000
		{"lb   ", "x7, (x0)     ", 0},
		{"lb   ", "x7, 0x10(x29)", 0x11},
		{"lb   ", "x7, 0x20(x29)", 0x68},
		{"lb   ", "x7, 0x40(x29)", 0x78},
		{"lb   ", "x7, 0xa0(x29)", 0xffffff81}, // sign extends
		{"lb   ", "x7, 0xb0(x29)", 0xffffff86}, // sign extends
		{"lbu  ", "x7, 0xa0(x29)", 0x00000081},
		{"lbu  ", "x7, 0xb0(x29)", 0x00000086},
		{"lh   ", "x7, 0x10(x29)", 0x11},
		{"lh   ", "x7, 0x20(x29)", 0x2468},
		{"lh   ", "x7, 0x40(x29)", 0x5678},
		{"lh   ", "x7, 0xa0(x29)", 0x00000081},
		{"lh   ", "x7, 0xb0(x29)", 0xffff8486}, // sign extends
		{"lhu  ", "x7, 0xa0(x29)", 0x00000081},
		{"lhu  ", "x7, 0xb0(x29)", 0x00008486},
		{"lw   ", "x7, 0x10(x29)", 0x11},
		{"lw   ", "x7, 0x20(x29)", 0x2468},
		{"lw   ", "x7, 0x40(x29)", 0x12345678},
		{"lw   ", "x7, 0xa0(x29)", 0x81},
		{"lw   ", "x7, 0xb0(x29)", 0x8486},
		{"lw   ", "x7, 0x8000(x29)", 0},
		{"lw   ", "x7, (x30)", 0},
	}

	for _, v := range cases {
		mnemonic := strings.TrimSpace(v.mnemonic)
		operand := strings.TrimSpace(v.operand)
		rd := registerMapping[strings.SplitN(operand, ",", 2)[0]]
		b := strings.Index(operand, "(")
		rs1 := -1
		if 0 <= b {
			rs1 = registerMapping[strings.TrimSpace(operand[b+1:strings.Index(operand, ")")])]
		}

		sim.load([][3]string{[...]string{"", mnemonic, operand}})
		sim.reset()
		sim.view.Disabled.Step = false
		sim.registers = x
		if 0 <= rs1 {
			sim.memory[x[rs1]] = m
		}

		beforePc := sim.pc

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newRequest("button=STEP"))

		if w.Code != http.StatusOK || sim.pc != beforePc+4 {
			t.Fatalf("%s %s Code = %d pc(diff)= %d", mnemonic, v.operand, w.Code, sim.pc-beforePc)
		}
		want := x
		want[rd] = v.want
		if sim.registers != want {
			t.Errorf("%s %s x%d = %x, want %x", mnemonic, v.operand, rd, sim.registers[rd], v.want)
		}
	}
}

func TestInstructionStore(t *testing.T) {
	handler, sim := newTestSimulatorHandler()

	var x [32]uint32
	for i := range x {
		x[i] = uint32(i*100 + i)
	}
	x[5] = 0x12345678
	x[28] = 0x20
	x[29] = 0x8000
	x[30] = 0xfffffff0

	cases := []struct {
		mnemonic string
		operand  string
		addr     uint32
		want     [4]byte
	}{
		{"sb", "x5, 0x10(x28)", 0x30, [...]byte{0x78, 0, 0, 0}},
		{"sh", "x5, 0x10(x28)", 0x30, [...]byte{0x78, 0x56, 0, 0}},
		{"sw", "x5, 0x10(x28)", 0x30, [...]byte{0x78, 0x56, 0x34, 0x12}},
		{"sb", "x5, 0x50(x29)", 0x8050, [...]byte{0x78, 0, 0, 0}},
		{"sh", "x5, 0x50(x29)", 0x8050, [...]byte{0x78, 0x56, 0, 0}},
		{"sw", "x5, 0x50(x29)", 0x8050, [...]byte{0x78, 0x56, 0x34, 0x12}},
		{"sb", "x5, (x30)", 0xfffffff0, [...]byte{0x78, 0, 0, 0}},
		{"sh", "x5, (x30)", 0xfffffff0, [...]byte{0x78, 0x56, 0, 0}},
		{"sw", "x5, (x30)", 0xfffffff0, [...]byte{0x78, 0x56, 0x34, 0x12}},
	}

	for _, v := range cases {
		sim.load([][3]string{[...]string{"", v.mnemonic, v.operand}})
		sim.reset()
		sim.view.Disabled.Step = false
		sim.registers = x

		beforePc := sim.pc

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newRequest("button=STEP"))

		if w.Code != http.StatusOK || sim.pc != beforePc+4 {
			t.Fatalf("%s %s Code = %d pc(diff) = %d", v.mnemonic, v.operand, w.Code, sim.pc-beforePc)
		}
		if sim.registers != x {
			t.Errorf("%s %s registers = %x, want %x", v.mnemonic, v.operand, sim.registers, x)
		}
		m := sim.memory[v.addr&0xffffff00]
		got := [...]byte{m[v.addr&0xff], m[(v.addr+1)&0xff], m[(v.addr+2)&0xff], m[(v.addr+3)&0xff]}
		if got != v.want {
			t.Errorf("%s %s memory = %x, want %x", v.mnemonic, v.operand, m, v.want)
		}
	}
}

func TestInstructionJump(t *testing.T) {
	handler, sim := newTestSimulatorHandler()

	var x [32]uint32
	for i := range x {
		x[i] = uint32(i*100 + i)
	}
	x[3] = sim.entryPoint + 0x80
	labelMap := map[string]uint32{}
	labelMap["l1"] = sim.entryPoint + 0x80

	cases := []struct {
		mnemonic string
		operand  string
		want     uint32
	}{
		{"jal", "x2, l1", labelMap["l1"]},
		{"jal", ", l1", labelMap["l1"]},
		{"jal", "l1", labelMap["l1"]},
		{"jalr", "x2, (x3)", x[3]},
		{"jalr", ", (x3)", x[3]},
		{"jalr", "(x3)", x[3]},
		{"jalr", "x2, 0x10(x3)", x[3] + 0x10},
		{"jalr", "x2, 0(x3)", x[3]},
		{"jalr", "x2, -4(x3)", x[3] - 4},
	}

	for _, v := range cases {
		rd, ok := registerMapping[strings.SplitN(v.operand, ",", 2)[0]]
		if !ok {
			rd = 1 // x1
		}

		sim.load([][3]string{[...]string{"", v.mnemonic, v.operand}})
		sim.reset()
		sim.view.Disabled.Step = false
		sim.registers = x
		sim.labelMapping = labelMap

		beforePc := sim.pc

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newRequest("button=STEP"))

		if w.Code != http.StatusOK {
			t.Fatalf("%s %s Code = %d", v.mnemonic, v.operand, w.Code)
		}
		if sim.pc != v.want {
			t.Errorf("%s %s pc = %x, want %x", v.mnemonic, v.operand, sim.pc, v.want)
		}
		want := x
		want[rd] = beforePc + 4
		if sim.registers != want {
			t.Errorf("%s %s registers = %x, want %x", v.mnemonic, v.operand, sim.registers, want)
		}
	}
}

func TestInstructionBranch(t *testing.T) {
	handler, sim := newTestSimulatorHandler()

	var x [32]uint32
	for i := range x {
		x[i] = uint32(i*100 + i)
	}
	x[5] = x[6]
	x[8] = 0xfffffff8
	labelMap := map[string]uint32{}
	labelMap["l1"] = sim.entryPoint + 0x40

	base := sim.entryPoint
	cases := []struct {
		mnemonic string
		operand  string
		want     uint32
	}{
		{"beq ", "x5, x6, l1", labelMap["l1"]},
		{"beq ", "x5, x7, l1", base + 4},
		{"bne ", "x5, x6, l1", base + 4},
		{"bne ", "x5, x7, l1", labelMap["l1"]},
		{"blt ", "x5, x4, l1", base + 4},
		{"blt ", "x5, x6, l1", base + 4},
		{"blt ", "x5, x7, l1", labelMap["l1"]},
		{"blt ", "x8, x9, l1", labelMap["l1"]},
		{"bltu", "x5, x4, l1", base + 4},
		{"bltu", "x5, x6, l1", base + 4},
		{"bltu", "x5, x7, l1", labelMap["l1"]},
		{"bltu", "x8, x9, l1", base + 4},
		{"bge ", "x5, x4, l1", labelMap["l1"]},
		{"bge ", "x5, x6, l1", labelMap["l1"]},
		{"bge ", "x5, x7, l1", base + 4},
		{"bge ", "x8, x9, l1", base + 4},
		{"bgeu", "x5, x4, l1", labelMap["l1"]},
		{"bgeu", "x5, x6, l1", labelMap["l1"]},
		{"bgeu", "x5, x7, l1", base + 4},
		{"bgeu", "x8, x9, l1", labelMap["l1"]},
	}

	for _, v := range cases {
		mnemonic := strings.TrimSpace(v.mnemonic)

		sim.load([][3]string{[...]string{"", mnemonic, v.operand}})
		sim.reset()
		sim.view.Disabled.Step = false
		sim.registers = x
		sim.labelMapping = labelMap

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, newRequest("button=STEP"))

		if w.Code != http.StatusOK {
			t.Fatalf("%s %s Code = %d", mnemonic, v.operand, w.Code)
		}
		if sim.pc != v.want {
			t.Errorf("%s %s pc = %x, want %x", mnemonic, v.operand, sim.pc, v.want)
		}
		if sim.registers != x {
			t.Errorf("%s %s registers = %x, want %x", mnemonic, v.operand, sim.registers, x)
		}
	}
}

func TestValidateInstruction(t *testing.T) {
	_, sim := newTestSimulatorHandler()

	cases := []struct {
		mnemonic []string
		operand  string
		want     int
	}{
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x2, x3, x4", 0},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x32, x33, 4", 3},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x32, x3, x4", 1},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x2, x33, x4", 1},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x2, x3, 4", 1},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "", 1},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x2", 1},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x2, x3", 1},
		{[]string{"add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu"}, "x2, x3, x4, x5", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, 4", 0},
		{[]string{"addi", "andi", "ori", "xori"}, "x32, x33, x4", 3},
		{[]string{"addi", "andi", "ori", "xori"}, "x32, x3, 4", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x33, 4", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, x4", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, 4, 5", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, 2047", 0}, // 12 bit
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, 2048", 1},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, -2048", 0},
		{[]string{"addi", "andi", "ori", "xori"}, "x2, x3, -2049", 1},
		{[]string{"sb", "sh", "sw"}, "x2, 0x20(x3)", 0},
		{[]string{"sb", "sh", "sw"}, "x2x, 0x20x(x3x)", 3},
		{[]string{"sb", "sh", "sw"}, "x2x, 0x20(x3)", 1},
		{[]string{"sb", "sh", "sw"}, "x2, 0x20x(x3)", 1},
		{[]string{"sb", "sh", "sw"}, "x2, 0x20(x3x)", 1},
		{[]string{"sb", "sh", "sw"}, "x2, 0x20", 1},
		{[]string{"sb", "sh", "sw"}, "x2", 1},
		{[]string{"sb", "sh", "sw"}, "x2, (x3)", 0},
		{[]string{"sb", "sh", "sw"}, "x2, 0x20(x3), 1", 1},
		{[]string{"sb", "sh", "sw"}, "", 1},
		{[]string{"sb", "sh", "sw"}, "x2, 2047(x3)", 0}, // 12 bit
		{[]string{"sb", "sh", "sw"}, "x2, 2048(x3)", 1},
		{[]string{"sb", "sh", "sw"}, "x2, -2048(x3)", 0},
		{[]string{"sb", "sh", "sw"}, "x2, -2049(x3)", 1},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5, x6, l1", 0},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5x, x6x, l2", 3},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5x, x6, l1", 1},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5, x6x, l1", 1},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5, x6, l2", 1},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5", 1},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5, x6", 1},
		{[]string{"beq", "bne", "blt", "bltu", "bge", "bgeu"}, "x5, x6, l1, 1", 1},
		{[]string{"beq"}, "", 1},
		{[]string{"lui", "auipc"}, "x30, 0x8", 0},
		{[]string{"lui", "auipc"}, "", 1},
		{[]string{"lui", "auipc"}, "30, x8", 2},
		{[]string{"lui", "auipc"}, "30, 0x8", 1},
		{[]string{"lui", "auipc"}, "x30, x8", 1},
		{[]string{"lui", "auipc"}, "x30, 0x80000", 0},
		{[]string{"lui", "auipc"}, "x30, 0x8ffff", 0},
		{[]string{"lui", "auipc"}, "x30, 0xfffff", 0}, // 20 bit
		{[]string{"lui", "auipc"}, "x30, 0x100000", 1},
		{[]string{"jal"}, "x2, l1", 0},
		{[]string{"jal"}, ", l1", 0},
		{[]string{"jal"}, "l1", 0},
		{[]string{"jal"}, "", 1},
		{[]string{"jal"}, "2, l2", 2},
		{[]string{"jal"}, "2, l1", 1},
		{[]string{"jal"}, "x5, l2", 1},
		{[]string{"jal"}, "x2, l1,", 1},
		{[]string{"jalr"}, "x5, 0x20(x3)", 0},
		{[]string{"jalr"}, ", 0x20(x3)", 0},
		{[]string{"jalr"}, "0x20(x3)", 0},
		{[]string{"jalr"}, "2, x20(x)", 3},
		{[]string{"jalr"}, "2, 0x20(x3)", 1},
		{[]string{"jalr"}, "x5, x20(x3)", 1},
		{[]string{"jalr"}, "x5, 0x20(x)", 1},
		{[]string{"jalr"}, "", 1},
		{[]string{"jalr"}, "x5, 2047(x3)", 0}, // 12 bit
		{[]string{"jalr"}, "x5, 2048(x3)", 1},
		{[]string{"jalr"}, "x5, -2048(x3)", 0},
		{[]string{"jalr"}, "x5, -2049(x3)", 1},
		{[]string{"jalr"}, "x5, 0x20(x3),", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, 4", 0},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x32, x33, x4", 3},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x32, x3, 4", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x33, 4", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, x4", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, 4, 5", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, -2048", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, -1", 1},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, 0", 0},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, 1", 0},
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, 31", 0}, // 5 bit
		{[]string{"slli", "srli", "srai", "slti", "sltiu"}, "x2, x3, 32", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 0x20(x3)", 0},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2x, 0x20x(x3x)", 3},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2x, 0x20(x3)", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 0x20x(x3)", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 0x20(x3x)", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 0x20", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 0x20(x3), 1", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 2047(x3)", 0}, // 12 bit
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, 2048(x3)", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, -2048(x3)", 0},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2, -2049(x3)", 1},
		{[]string{"lbu", "lb", "lhu", "lh", "lw"}, "x2	 ,	 2047  (	x3	 )", 0},
		{[]string{"ecall", "ebreak", "fence", "csrrw", "csrrs", "csrrc", "csrrwi", "csrrsi", "csrrci", "fence.i", "123"}, "", 1},
	}

	for _, v := range cases {
		for _, mnemonic := range v.mnemonic {
			rec := &StringRecorder{[]string{}}
			sim.validationError = rec

			valid := sim.validate([][3]string{[...]string{"l1:", mnemonic, v.operand}})
			if valid != (v.want == 0) || len(rec.messages) != v.want {
				t.Errorf("%s %s valid=%v(%d)", mnemonic, v.operand, valid, len(rec.messages))
			}
		}
	}
}

func TestSplitLine(t *testing.T) {
	cases := []struct {
		line                            string
		definedLabel, mnemonic, operand string
	}{
		{``, "", "", ""},
		{`.text`, "", "", ""},
		{`.l1:`, ".l1:", "", ""},
		{`addi x5,x5,1`, "", "addi", "x5,x5,1"},
		{`addi x5, x5, 1`, "", "addi", "x5, x5, 1"},
		{`addi x5,  x5,  1`, "", "addi", "x5,  x5,  1"},
		{`	addi		x5,x5,1`, "", "addi", "x5,x5,1"},
		{`L1: addi x5, x5, 1 ; testcase`, "L1:", "addi", "x5, x5, 1"},
		{`La0:	 Lb  	x2	 ,	 2047  (	x3	 ) 	`, "La0:", "Lb", "x2	 ,	 2047  (	x3	 )"},
		{`	 Lb  	x2	 ,	 2047  (	x3	 ) 	`, "", "Lb", "x2	 ,	 2047  (	x3	 )"},
		{`L1:`, "L1:", "", ""},
		{`# testcase`, "", "", ""},
		{`fence`, "", "fence", ""},
	}
	for _, v := range cases {
		splited := splitLine(v.line)
		definedLabel, mnemonic, operand := splited[0], splited[1], splited[2]
		if definedLabel != v.definedLabel || mnemonic != v.mnemonic || operand != v.operand {
			t.Errorf(v.line)
		}
	}
}

func TestValidateDefinedLabel(t *testing.T) {
	_, sim := newTestSimulatorHandler()

	cases := []struct {
		definedLabel string
		want         bool
	}{
		{"label1:", true},
		{"Label1:", true},
		{"_Label1:", true},
		{"Label1_:", true},
		{"Label.1:", true},
		{"Label$1:", true},
		{"a:", true},
		{"z:", true},
		{"A:", true},
		{"Z:", true},
		{"l0:", true},
		{"l9:", true},
		{".1:", true},
		{"$1:", true},
		{"Label+1:", false},
		{"1label:", false},
		{"label1", false},
		{"label1", false},
		{":", false},
		{":l", false},
		{strings.Join(make([]string, 4097), "a") + ":", false},
	}

	for _, v := range cases {
		valid := sim.validate([][3]string{[...]string{v.definedLabel, "", ""}})
		if valid != v.want {
			t.Errorf("%s %v", v.definedLabel, valid)
		}
	}
}

func TestValidateDefinedLabelDuplicated(t *testing.T) {
	_, sim := newTestSimulatorHandler()

	lines := [][3]string{
		[...]string{"l1:", "addi", "x5, x0, 1"},
		[...]string{"l1:", "addi", "x5, x5, 1"},
	}

	if sim.validate(lines) != false {
		t.Errorf("was through. [%s]", lines[0][0])
	}
}

func TestFormat(t *testing.T) {
	cases := []struct {
		s    string
		want string
	}{
		{"", ""},
		{"1", "1"},
		{"123456789", "123456789"},
		{"1234567890", "1234567890"},
		{"12345678901", "12345678901"},
		{"123456789012", "123456789012"},
		{"1234567890123", "1234567890123"},
		{"12345678901234", "1234567890..."},
		{"123456789012345", "1234567890..."},
		{"1234567890123456", "1234567890..."},
	}

	for _, v := range cases {
		got := format(v.s, 13)
		if got != v.want {
			t.Errorf("%s, want %s", got, v.want)
		}
	}
}

func TestStatusCode(t *testing.T) {
	handler, _ := newTestSimulatorHandler()

	cases := []struct {
		method string
		target string
		body   string
		want   int
	}{
		{"GET", "/", "", http.StatusOK},
		{"OPTIONS", "/", "", http.StatusMethodNotAllowed},
		{"HEAD", "/", "", http.StatusMethodNotAllowed},
		{"PUT", "/", "", http.StatusMethodNotAllowed},
		{"PATCH", "/", "", http.StatusMethodNotAllowed},
		{"DELETE", "/", "", http.StatusMethodNotAllowed},
		{"TRACE", "/", "", http.StatusMethodNotAllowed},
		{"CONNECT", "/", "", http.StatusMethodNotAllowed},
		{"GET", "/a", "", http.StatusNotFound},
		{"POST", "/a", "", http.StatusNotFound},
		{"GET", "/?", "", http.StatusNotFound},
		{"POST", "/?", "", http.StatusNotFound},
		{"GET", "/?button=STOP", "", http.StatusNotFound},
		{"POST", "/?button=STOP", "", http.StatusNotFound},
		{"POST", "/", "12345678901234567", http.StatusRequestEntityTooLarge},
		{"GET", "/", "a", http.StatusBadRequest},
		{"POST", "/", "", http.StatusBadRequest},
		{"POST", "/", "a", http.StatusBadRequest},
		{"POST", "/", "button=START", http.StatusBadRequest},
	}

	for _, v := range cases {
		var body io.Reader
		if v.body != "" {
			body = strings.NewReader(v.body)
		}

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(v.method, v.target, body))

		if w.Code != v.want {
			t.Errorf("%s %s %s Code = %d, want %d", v.method, v.target, v.body, w.Code, v.want)
		}
	}
}

func TestDisabled(t *testing.T) {
	handler, sim := newTestSimulatorHandler()

	cases := []struct {
		button   string
		disabled DisabledButton
		want     int
	}{
		{"button=RUN", DisabledButton{true, false, false, false}, http.StatusBadRequest},
		{"button=STEP", DisabledButton{false, true, false, false}, http.StatusBadRequest},
		{"button=STOP", DisabledButton{false, false, true, false}, http.StatusBadRequest},
		{"button=STOP", DisabledButton{true, true, false, true}, http.StatusOK},
		{"button=RELOAD", DisabledButton{false, false, false, true}, http.StatusBadRequest},
		{"button=", DisabledButton{true, true, true, true}, http.StatusBadRequest},
	}

	for _, v := range cases {
		sim.view.Disabled = v.disabled
		sim.view.Codes[0].Address = fmt.Sprintf("0x%08x", sim.entryPoint)

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "/", strings.NewReader(v.button)))

		if w.Code != v.want {
			t.Errorf("%s Code = %d", v.button, w.Code)
		}
	}
}

func TestResponseHeader(t *testing.T) {
	handler, _ := newTestSimulatorHandler()

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	if w.Code != http.StatusOK {
		t.Errorf("Code=%d", w.Code)
	}
	headers := []struct {
		name string
		want string
	}{
		{"Content-Length", strconv.Itoa(len(w.Body.Bytes()))},
		{"Content-Type", `text/html; charset=utf-8`},
		{"Cache-Control", `no-cache, no-store, max-age=0, private, must-revalidate`},
		{"Pragma", `no-cache`},
		{"Expires", "0"},
		{"X-Content-Type-Options", `nosniff`},
		{"X-Frame-Options", `DENY`},
		{"X-XSS-Protection", `1; mode=block`},
		{"Content-Security-Policy", `default-uri 'none';`},
	}
	for _, v := range headers {
		got := w.Header().Values(v.name)
		if len(got) != 1 {
			t.Errorf("%s size = %d", v.name, len(got))
		} else if got[0] != v.want {
			t.Errorf("%s: %s want %s", v.name, got[0], v.want)
		}
	}
}

func TestRun(t *testing.T) {
	handler, sim := newTestSimulatorHandler()

	lines := [][3]string{}
	lines = append(lines, [...]string{"l1:", "addi", "x5, x0, 1"})
	for i := 2; i <= len(sim.view.Codes); i++ {
		lines = append(lines, [...]string{fmt.Sprintf("l%d:", i), "addi", "x5, x5, 1"})
	}
	lines = append(lines, [...]string{"end:", "", ""})

	sim.load(lines)
	sim.reset()
	sim.view.Disabled.Run = false

	beforePc := sim.pc

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newRequest("button=RUN"))

	if w.Code != 200 {
		t.Fatalf("Code = %d", w.Code)
	}
	if sim.pc != beforePc+uint32(len(lines)*4) {
		t.Errorf("pc = %x", sim.pc)
	}
	want := [32]uint32{}
	want[5] = uint32(len(sim.instructions) - 1)
	if sim.registers != want {
		t.Errorf("registers = %x, want %x", sim.registers, want)
	}
}

func TestStop(t *testing.T) {
	handler, sim := newTestSimulatorHandler()
	sim.view.Disabled.Stop = false
	sim.view.Codes[0].Address = fmt.Sprintf("0x%08x", sim.entryPoint)

	sim.pc = sim.entryPoint + 4
	sim.last = &Effect{}
	sim.labelMapping = map[string]uint32{}
	want := uint32(20)
	sim.labelMapping["l1"] = want
	var x [32]uint32
	for i := range x {
		x[i] = uint32(i*100 + i)
	}
	sim.registers = x
	m := make([]byte, 16*16)
	m[0x10] = 0x11
	sim.memory = map[uint32][]byte{}
	sim.memory[0] = m
	sim.memory[16*16] = m
	sim.memory[16*16*2] = m
	sim.memory[16*16*3] = m

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, newRequest("button=STOP"))

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d", w.Code)
	}
	if sim.pc != sim.entryPoint {
		t.Error("pc not reset")
	}
	if sim.registers != [32]uint32{} {
		t.Error("registers not reset")
	}
	if sim.last != nil {
		t.Error("last not reset")
	}
	if len(sim.labelMapping) == 0 || sim.labelMapping["l1"] != want {
		t.Error("labelMapping reset")
	}
	if len(sim.memory) != 2 {
		t.Error("memory not reset")
	} else if _, ok := sim.memory[0]; !ok {
		t.Error("memory not reset")
	} else if _, ok := sim.memory[16*16]; !ok {
		t.Error("memory not reset")
	}
}

func TestScrollViewInstruction(t *testing.T) {
	_, sim := newTestSimulatorHandler()

	cases := []struct {
		instructionSize int
		current         int
		addr            uint32
		want            int
	}{
		{0, 0, sim.entryPoint, 0},
		{14, 0, sim.entryPoint, 0},
		{15, 0, sim.entryPoint, 0},
		{16, 0, sim.entryPoint, 0},
		{17, 0, sim.entryPoint, 0},
		{18, 0, sim.entryPoint, 0},
		{31, 0, sim.entryPoint, 0},
		{32, 0, sim.entryPoint, 0},
		{32, 14, sim.entryPoint, 0},
		{32, 15, sim.entryPoint, 0},
		{32, 16, sim.entryPoint, 0},
		{32, 17, sim.entryPoint, 0},
		{32, 18, sim.entryPoint, 0},
		{32, 31, sim.entryPoint, 0},
		{32, 32, sim.entryPoint, 0},
		{33, 0, sim.entryPoint, 0},
		{33, 14, sim.entryPoint, 0},
		{33, 15, sim.entryPoint, 0},
		{33, 16, sim.entryPoint, 0},
		{33, 17, sim.entryPoint, 1},
		{33, 18, sim.entryPoint, 1},
		{33, 31, sim.entryPoint, 1},
		{33, 32, sim.entryPoint, 1},
		{33, 33, sim.entryPoint, 1},
		{34, 0, sim.entryPoint, 0},
		{34, 14, sim.entryPoint, 0},
		{34, 15, sim.entryPoint, 0},
		{34, 16, sim.entryPoint, 0},
		{34, 17, sim.entryPoint, 1},
		{34, 18, sim.entryPoint, 2},
		{34, 31, sim.entryPoint, 2},
		{34, 32, sim.entryPoint, 2},
		{34, 33, sim.entryPoint, 2},
		{34, 34, sim.entryPoint, 2},
		{35, 0, sim.entryPoint, 0},
		{35, 14, sim.entryPoint, 0},
		{35, 15, sim.entryPoint, 0},
		{35, 16, sim.entryPoint, 0},
		{35, 17, sim.entryPoint, 1},
		{35, 18, sim.entryPoint, 2},
		{35, 31, sim.entryPoint, 3},
		{35, 32, sim.entryPoint, 3},
		{35, 33, sim.entryPoint, 3},
		{35, 34, sim.entryPoint, 3},
		{35, 35, sim.entryPoint, 3},
		{99, 24, sim.entryPoint + (10 * 4), 10},
		{99, 25, sim.entryPoint + (10 * 4), 10},
		{99, 26, sim.entryPoint + (10 * 4), 10},
		{99, 27, sim.entryPoint + (10 * 4), 11},
		{99, 28, sim.entryPoint + (10 * 4), 12},
	}

	for _, v := range cases {
		sim.instructions = make([]Instruction, v.instructionSize)
		sim.view.Codes[0].Address = fmt.Sprintf("0x%08x", v.addr)
		sim.scrollViewInstruction(v.current)
		if sim.instructionViewBase() != v.want {
			t.Errorf("instructionViewBase = %d, want %d", sim.instructionViewBase(), v.want)
		}
	}
}

func TestFocusViewMemoryRange(t *testing.T) {
	_, sim := newTestSimulatorHandler()

	cases := []struct {
		base     uint32
		memRead  []uint32
		memWrite []uint32
		want     uint32
	}{
		{0, nil, nil, 0},
		{0x100, nil, nil, 0x100},
		{0x200, nil, nil, 0x200},
		{0x300, nil, nil, 0x300},
		{0x7fffff00, nil, nil, 0x7fffff00},
		{0x80000000, nil, nil, 0x80000000},
		{0xfffffd00, nil, nil, 0xfffffd00},
		{0xfffffe00, nil, nil, 0xfffffe00},
		{0, []uint32{0}, nil, 0},
		{0, []uint32{0x1ff}, nil, 0},
		{0, []uint32{0x2ff}, nil, 0x200},
		{0, []uint32{0x1ff, 0x200}, nil, 0x100},
		{0x100, []uint32{0}, nil, 0},
		{0x100, []uint32{0xff}, nil, 0},
		{0x100, []uint32{0x100}, nil, 0x100},
		{0x100, []uint32{0x2ff}, nil, 0x100},
		{0x100, []uint32{0x300}, nil, 0x300},
		{0x200, []uint32{0}, nil, 0},
		{0x200, []uint32{0xff}, nil, 0},
		{0x200, []uint32{0x100}, nil, 0},
		{0x200, []uint32{0x1ff}, nil, 0},
		{0x200, []uint32{0x200}, nil, 0x200},
		{0x200, []uint32{0x2ff}, nil, 0x200},
		{0x200, []uint32{0x300}, nil, 0x200},
		{0x200, []uint32{0x3ff}, nil, 0x200},
		{0x200, []uint32{0x400}, nil, 0x400},
		{0x8000, []uint32{0}, nil, 0},
		{0x8000, []uint32{0x100}, nil, 0},
		{0x8000, []uint32{0x1ff}, nil, 0},
		{0x8000, []uint32{0x200}, nil, 0x200},
		{0x8000, []uint32{0x2ff}, nil, 0x200},
		{0x8000, []uint32{0x300}, nil, 0x300},
		{0x8000, []uint32{0x300}, nil, 0x300},
		{0x8000, []uint32{0x7fff}, nil, 0x7f00},
		{0x8000, []uint32{0x8000}, nil, 0x8000},
		{0x8000, []uint32{0x81ff}, nil, 0x8000},
		{0x8000, []uint32{0x8200}, nil, 0x8200},
		{0x8000, []uint32{0xfffffdff}, nil, 0xfffffd00},
		{0x8000, []uint32{0xfffffe00}, nil, 0xfffffe00},
		{0x8000, []uint32{0xfffffeff}, nil, 0xfffffe00},
		{0x8000, []uint32{0xffffffff}, nil, 0xfffffe00},
		{0x8000, []uint32{0x1ff, 0x200}, nil, 0x100},
		{0, nil, []uint32{0}, 0},
		{0x8000, nil, []uint32{0x7fff, 0x8000}, 0x7f00},
		{0x8000, nil, []uint32{0x8000, 0x8001}, 0x8000},
		{0x8000, nil, []uint32{0x81fe, 0x81ff}, 0x8000},
		{0x8000, nil, []uint32{0x81ff, 0x8200}, 0x8100},
	}

	for _, v := range cases {
		sim.memory = map[uint32][]byte{}
		for i := range sim.view.Mems {
			sim.view.Mems[i].BaseAddress = fmt.Sprintf("0x%08x", v.base+uint32(i*16))
		}
		if sim.view.memoryBase() != v.base {
			t.Fatalf("memoryBase = %x, want %x", sim.view.memoryBase(), v.base)
		}
		effect := &Effect{
			MemRead:  v.memRead,
			MemWrite: v.memWrite,
		}

		sim.focusViewMemoryRange(effect)

		if sim.view.memoryBase() != v.want {
			t.Errorf("memoryBase = %x, want %x", sim.view.memoryBase(), v.want)
		}
	}
}
