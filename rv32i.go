package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// config
const (
	host         = "localhost" // personal use
	port         = "8532"      // FYI 8000:web 5:RISC-V 32:RV32I
	entryPoint   = 0x1000      // just an idea. look well
	timeoutSec   = 5           // force suspend. for infinite loop detection
	HSTS         = false       // if https then set true
	labelWidth   = 14          // 8 <= labelWidth <= 25
	operandWidth = 24          // 18 <= operandWidth <= 50
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("no filename")
	}
	fileName := os.Args[1] // ignore after the 2nd

	handler := NewSimulatorHandler(fileName, entryPoint)
	handler.init("shared")

	server := http.Server{
		Addr:    host + ":" + port,
		Handler: handler,
	}
	if err := server.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

type SimulatorHandler struct {
	mu sync.Mutex

	sims     map[string]*Simulator // designed with 1:n data structure and used 1:1. shared only
	sharedId string

	fileName   string
	entryPoint uint32
	singlePage *template.Template
}

type Simulator struct {
	mu sync.Mutex

	fileName string

	entryPoint   uint32
	end          *uint32
	labelMapping map[string]uint32
	instructions []Instruction

	pc        uint32
	registers [32]uint32
	memory    map[uint32][]byte

	last *Effect

	view       SinglePageView
	singlePage *template.Template

	validationError io.Writer
}

type Instruction struct {
	Label       string
	MnemonicRaw string
	Mnemonic    string
	Operand     string
}

type Effect struct {
	Current int
	Ref     int
	Jump    bool

	Rd  int
	Rs1 int
	Rs2 int

	RdS, RdU bool
	RsS, RsU bool

	MemRead  []uint32
	MemWrite []uint32
}

type SinglePageView struct {
	InstructionWidth [4]string
	RegisterWidth    [8]string
	MemoryWidth      [17]string
	MemoryOffset     [16]string

	Codes [32]InstructionRow
	Regs  [32]RegisterRow
	Mems  [32]MemoryRow

	Disabled DisabledButton
	Step     bool
	Failed   bool
	Timeout  bool
}

type InstructionRow struct {
	Address  string
	Label    string
	Mnemonic string
	Operand  string

	Current  bool
	RefColor string
}

type RegisterRow struct {
	Name string
	ABI  string

	Signed   string
	Unsigned string
	Bin      string
	Hex      string

	Even           bool
	Color          string
	SignedUnused   bool
	UnsignedUnused bool
}

type MemoryRow struct {
	BaseAddress string
	Bytes       [16]MemoryValue
}

type MemoryValue struct {
	Hex   string
	Color string
}

type DisabledButton struct {
	Run    bool
	Step   bool
	Stop   bool
	Reload bool
}

const (
	RUN    = "button=RUN"
	STEP   = "button=STEP"
	STOP   = "button=STOP"
	RELOAD = "button=RELOAD"

	ColorRead  = "blue"
	ColorWrite = "red"

	ra = "x1" // The standard software calling convention uses x1 as the return address register
)

var (
	abiNames        [32]string
	registerMapping map[string]int

	standby, ready, running, executed DisabledButton
)

func init() {
	abiNames = [...]string{
		"zero",  // Hard-wired zero
		"ra",    // Return address
		"sp",    // Stack pointer
		"gp",    // Global pointer
		"tp",    // Thread pointer
		"t0",    // Temporary/alternate link register
		"t1",    // Temporaries
		"t2",    // Temporaries
		"s0/fp", // Saved register/frame pointer
		"s1",    // Saved register
		"a0",    // Function arguments/return values
		"a1",    // Function arguments/return values
		"a2",    // Function arguments
		"a3",    // Function arguments
		"a4",    // Function arguments
		"a5",    // Function arguments
		"a6",    // Function arguments
		"a7",    // Function arguments
		"s2",    // Saved registers
		"s3",    // Saved registers
		"s4",    // Saved registers
		"s5",    // Saved registers
		"s6",    // Saved registers
		"s7",    // Saved registers
		"s8",    // Saved registers
		"s9",    // Saved registers
		"s10",   // Saved registers
		"s11",   // Saved registers
		"t3",    // Temporaries
		"t4",    // Temporaries
		"t5",    // Temporaries
		"t6",    // Temporaries
	}

	registerMapping = map[string]int{}
	for i, v := range abiNames {
		registerMapping[fmt.Sprintf("x%d", i)] = i
		n := strings.Index(v, "/")
		if n == -1 {
			registerMapping[v] = i
		} else {
			registerMapping[v[:n]] = i
			registerMapping[v[n+1:]] = i
		}
	}

	standby = DisabledButton{true, true, true, false}
	ready = DisabledButton{false, false, true, false}
	running = DisabledButton{false, false, false, true}
	executed = DisabledButton{true, true, false, true}
}

func NewSimulatorHandler(fileName string, entryPoint uint32) *SimulatorHandler {
	return &SimulatorHandler{
		sims:       map[string]*Simulator{},
		fileName:   fileName,
		entryPoint: (min(entryPoint, 0xffffff80) + 3) & 0xfffffffc, // aligned on a four byte boundary
		singlePage: template.Must(template.New("singlePage").Parse(simulatorHTML[1:])),
	}
}

func (h *SimulatorHandler) init(sharedId string) {
	h.sharedId = sharedId
	h.sharedSimulator() // create
}

func (h *SimulatorHandler) sharedSimulator() *Simulator {
	return h.findOrCreateSimulator(h.sharedId)
}

func (h *SimulatorHandler) findOrCreateSimulator(id string) *Simulator {
	if id == "" {
		id = h.sharedId // default
	}
	if id == h.sharedId {
		if sim, ok := h.sims[id]; ok {
			return sim
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sims[id]; !ok {
		h.sims[id] = h.newSimulator()
	}
	return h.sims[id]
}

func (h *SimulatorHandler) newSimulator() *Simulator {
	sim := NewSimulator(
		h.fileName,
		h.entryPoint,
		h.singlePage,
		os.Stderr,
	)
	sim.init()
	if sim.end == nil {
		sim.view.setStatus(standby)
	} else {
		sim.view.setStatus(ready)
	}
	return sim
}

func NewSimulator(fileName string, entryPoint uint32, singlePage *template.Template, w io.Writer) *Simulator {
	sim := Simulator{
		fileName:        fileName,
		entryPoint:      entryPoint,
		singlePage:      singlePage,
		validationError: w,
	}

	padding := strings.Join(make([]string, max(70, max(labelWidth, operandWidth))+1), "_")

	const address = len("0x00000000") + 2
	const label = max(8, min(25, labelWidth))
	const mnemonic = 8
	const operand = max(18, min(50, operandWidth))
	for i, v := range [...]int{address, label, mnemonic, operand} {
		sim.view.InstructionWidth[i] = padding[:v]
	}

	const name = len("x10") + 2
	const abi = 5
	signed := len(strconv.FormatInt(math.MinInt32, 10)) + 2
	unsigned := len(strconv.FormatUint(math.MaxUint32, 10)) + 2
	const bin = 32 + 1
	const hex = len("00000000") + 2
	for i, v := range [...]int{name, 1, abi, 1, signed, unsigned, bin, hex} {
		sim.view.RegisterWidth[i] = padding[:v]
	}

	for i := range sim.view.Regs {
		sim.view.Regs[i].Name = fmt.Sprintf("x%d", i)
		sim.view.Regs[i].ABI = abiNames[i]
		sim.view.Regs[i].Even = (i % 2) == 0
	}

	for i := range sim.view.MemoryWidth {
		sim.view.MemoryWidth[i] = padding[:3]
	}
	sim.view.MemoryWidth[0] = padding[:13] // overwrite Address width

	for i := range sim.view.MemoryOffset {
		sim.view.MemoryOffset[i] = fmt.Sprintf("%02x", i)
	}

	return &sim
}

func (h *SimulatorHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" && r.Method != "GET" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.RequestURI != "/" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	buf := make([]byte, 16) // small margin
	n, err := r.Body.Read(buf)
	r.Body.Close()
	if r.Method == "GET" && 0 < n {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if (err != nil && err != io.EOF) || n == len(buf) {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}
	var body string
	if r.Method == "POST" {
		if n < 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body = string(buf[:n])
	}

	sim := h.sharedSimulator()

	sim.mu.Lock()
	defer sim.mu.Unlock()
	sim.Handle(w, body)
}

func (sim *Simulator) Handle(w http.ResponseWriter, req string) {
	if sim.view.wasDisabled(req) {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	var effect *Effect

	switch req {
	case RUN:
		timeLimit := time.Now().Add(timeoutSec * time.Second)
		for sim.effectivePc() {
			effect = sim.executeCurrent()
			sim.focusViewMemoryRange(effect)  // each
			if timeLimit.Before(time.Now()) { // timeLimit < now
				break
			}
		}
		sim.scrollViewInstruction(effect.Current)
		sim.syncView()
		sim.view.Step = false
		if sim.effectivePc() {
			sim.view.Timeout = true
			sim.view.setStatus(running)
		} else {
			sim.view.Timeout = false
			effect.Rd, effect.Rs1, effect.Rs2, effect.MemRead, effect.MemWrite = -1, -1, -1, nil, nil
			sim.view.setStatus(executed)
		}
	case STEP:
		effect = sim.executeCurrent()
		sim.scrollViewInstruction(effect.Current)
		sim.focusViewMemoryRange(effect)
		sim.view.Timeout = false
		if sim.effectivePc() {
			sim.view.Step = true
			sim.view.setStatus(running)
		} else {
			sim.view.Step = false
			sim.view.setStatus(executed)
		}
	case STOP:
		sim.reset()
		sim.view.setStatus(ready)
	case RELOAD:
		sim.init()
		if sim.end == nil {
			sim.view.setStatus(standby)
		} else {
			sim.view.setStatus(ready)
		}
	case "": // GET
		effect = sim.last // idempotence
	default:
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	sim.last = effect

	sim.sendResponse(w, effect)
}

func (sim *Simulator) sendResponse(w http.ResponseWriter, effect *Effect) {
	for i := range sim.view.Codes {
		sim.view.Codes[i].Current = false
		sim.view.Codes[i].RefColor = ""
	}
	for i := range sim.view.Regs {
		sim.view.Regs[i].Color = ""
		sim.view.Regs[i].SignedUnused = false
		sim.view.Regs[i].UnsignedUnused = false
	}
	for i := range sim.view.Mems {
		for j := range sim.view.Mems[i].Bytes {
			sim.view.Mems[i].Bytes[j].Color = ""
		}
	}

	if effect != nil {
		base := sim.instructionViewBase()
		sim.view.Codes[effect.Current-base].Current = true
		ref := effect.Ref - base
		if 0 <= ref && ref < len(sim.view.Codes) {
			if effect.Jump {
				sim.view.Codes[ref].RefColor = ColorWrite
			} else {
				sim.view.Codes[ref].RefColor = ColorRead
			}
		}

		rs1 := effect.Rs1
		if 0 <= rs1 {
			sim.view.Regs[rs1].Color = ColorRead
			sim.view.Regs[rs1].SignedUnused = !effect.RsS
			sim.view.Regs[rs1].UnsignedUnused = !effect.RsU
		}

		rs2 := effect.Rs2
		if 0 <= rs2 {
			sim.view.Regs[rs2].Color = ColorRead
			sim.view.Regs[rs2].SignedUnused = !effect.RsS
			sim.view.Regs[rs2].UnsignedUnused = !effect.RsU
		}

		rd := effect.Rd
		if 0 <= rd {
			v := sim.registers[rd]
			sim.view.Regs[rd].Signed = fmt.Sprintf("%d", int32(v))
			sim.view.Regs[rd].Unsigned = fmt.Sprintf("%d", v)
			sim.view.Regs[rd].Bin = fmt.Sprintf("%032b", v)
			sim.view.Regs[rd].Hex = fmt.Sprintf("%08x", v)
			sim.view.Regs[rd].Color = ColorWrite
			sim.view.Regs[rd].SignedUnused = !effect.RdS
			sim.view.Regs[rd].UnsignedUnused = !effect.RdU
		}

		if effect.MemRead != nil {
			for _, addr := range effect.MemRead {
				i, j := sim.view.memoryIndex(addr)
				sim.view.Mems[i].Bytes[j].Color = ColorRead
			}
		}
		if effect.MemWrite != nil {
			for _, addr := range effect.MemWrite {
				i, j := sim.view.memoryIndex(addr)
				sim.view.Mems[i].Bytes[j].Hex = fmt.Sprintf("%02x", sim.readMemory(addr))
				sim.view.Mems[i].Bytes[j].Color = ColorWrite
			}
		}
	}

	body := bytes.Buffer{}
	if err := sim.singlePage.Execute(&body, sim.view); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Fatal(err)
	}
	bodyBytes := body.Bytes()

	w.Header().Set("Content-Length", strconv.Itoa(len(bodyBytes)))
	w.Header().Set("Content-Type", `text/html; charset=utf-8`)
	w.Header().Set("Cache-Control", `no-cache, no-store, max-age=0, private, must-revalidate`)
	w.Header().Set("Pragma", `no-cache`)
	w.Header().Set("Expires", `0`)
	w.Header().Set("X-Content-Type-Options", `nosniff`)
	w.Header().Set("X-Frame-Options", `DENY`)
	w.Header().Set("X-XSS-Protection", `1; mode=block`)
	w.Header().Set("Content-Security-Policy", `default-uri 'none';`)
	if HSTS {
		w.Header().Set("Strict-Transport-Security", `max-age=31536000`)
	}

	if _, err := w.Write(bodyBytes); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		if errors.Is(err, syscall.EPIPE) {
			log.Print(err)
		} else {
			log.Fatal(err)
		}
	}
}

func (sim *Simulator) effectivePc() bool {
	return (sim.end != nil) && (sim.pc&3 == 0) && (sim.entryPoint <= sim.pc) && (sim.pc <= *sim.end)
}

func (sim *Simulator) currentInstructionIndex() int {
	return sim.instructionIndex(&sim.pc)
}

func (sim *Simulator) instructionIndex(addr *uint32) int {
	if addr == nil {
		return -1
	}
	return int((*addr - sim.entryPoint) / 4)
}

func (sim *Simulator) readMemory(addr uint32) byte {
	base := addr & 0xffffff00
	if _, ok := sim.memory[base]; !ok {
		sim.memory[base] = make([]byte, 16*16)
		return 0
	}
	offset := addr & 0xff
	return sim.memory[base][offset]
}

func (sim *Simulator) writeMemory(addr uint32, b byte) {
	base, offset := addr&0xffffff00, addr&0xff
	if _, ok := sim.memory[base]; !ok {
		sim.memory[base] = make([]byte, 16*16)
	}
	sim.memory[base][offset] = b
}

func (sim *Simulator) init() {
	lines := sim.readFile()
	valid := sim.validate(lines)
	if !valid {
		lines = [][3]string{}
	}
	sim.load(lines)
	sim.reset()
	sim.view.Failed = !valid
}

func (sim *Simulator) readFile() [][3]string {
	file, err := os.Open(sim.fileName)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	lines := [][3]string{}
	for s := bufio.NewScanner(file); s.Scan(); {
		lines = append(lines, splitLine(s.Text()))
	}

	return lines
}

func splitLine(line string) [3]string {
	trimed := strings.TrimSpace(line)

	if trimed == "" || (trimed[0] == '.' && strings.Index(trimed, ":") == -1) {
		return [3]string{}
	}

	// comment
	c := strings.IndexAny(trimed, "#;")
	if c == -1 {
		c = len(trimed)
	}

	definedLabel, mnemonic, operand := "", "", ""

	// label
	l := strings.Index(trimed[:c], ":")
	if l != -1 {
		l++
		definedLabel = strings.TrimSpace(trimed[:l]) // definedLabel = label + ":"
	} else {
		l = 0
	}

	// instruction
	instruction := strings.TrimSpace(trimed[l:c])
	if instruction != "" {
		i := strings.IndexAny(instruction, "\t ")
		if i == -1 {
			mnemonic = instruction
		} else {
			mnemonic = strings.TrimSpace(instruction[:i])
			operand = strings.TrimSpace(instruction[i:])
		}
	}

	return [3]string{definedLabel, mnemonic, operand}
}

func (sim *Simulator) load(validated [][3]string) {
	sim.labelMapping = map[string]uint32{}

	nextAddress := sim.entryPoint
	candiLabel := ""
	filtered := [][3]string{}
	for _, v := range validated {
		definedLabel, mnemonic, operand := v[0], v[1], v[2]
		if definedLabel == "" && mnemonic == "" {
			continue
		}
		if definedLabel != "" {
			label := definedLabel[:len(definedLabel)-1] // remove the trailing ':'
			sim.labelMapping[label] = nextAddress       // case-sensitive
		}
		if mnemonic == "" {
			candiLabel = definedLabel
			continue
		}
		if definedLabel == "" {
			definedLabel = candiLabel
		}
		filtered = append(filtered, [3]string{definedLabel, mnemonic, operand})
		nextAddress += 4
		candiLabel = ""
	}

	count := len(filtered)

	if candiLabel != "" && count != 0 {
		filtered = append(filtered, [3]string{candiLabel, "", ""})
		count++
	}

	sim.instructions = make([]Instruction, max(count, len(sim.view.Codes)))

	for i := range sim.instructions {
		definedLabel, mnemonic, operand := "", "", ""
		if i < count {
			definedLabel, mnemonic, operand = filtered[i][0], filtered[i][1], filtered[i][2]
		}
		sim.instructions[i].Label = definedLabel
		sim.instructions[i].MnemonicRaw = mnemonic
		sim.instructions[i].Mnemonic = normalizeMnemonic(mnemonic)
		sim.instructions[i].Operand = normalizeOperand(operand)
		if len(sim.view.Codes) <= i {
			continue
		}
		sim.view.Codes[i].Address = fmt.Sprintf("0x%08x", sim.entryPoint+uint32(i*4))
		sim.view.Codes[i].Label = formatLabel(definedLabel, sim.view.InstructionWidth)
		sim.view.Codes[i].Mnemonic = mnemonic
		sim.view.Codes[i].Operand = formatOperand(operand, sim.view.InstructionWidth)
	}

	if count == 0 {
		sim.end = nil
	} else {
		end := sim.entryPoint + uint32(count-1)*4
		sim.end = &end
	}
}

func normalizeMnemonic(mnemonic string) string {
	return strings.ToLower(mnemonic)
}

func normalizeOperand(operand string) string {
	return strings.ReplaceAll(strings.ReplaceAll(operand, "\t", ""), " ", "")
}

func formatLabel(definedLabel string, width [4]string) string {
	return format(definedLabel, len(width[1])-1)
}

func formatOperand(operand string, width [4]string) string {
	return format(strings.ReplaceAll(normalizeOperand(operand), ",", ", "), len(width[3])-2)
}

func format(s string, maxlen int) string {
	bytes := []byte(s)
	if len(bytes) <= maxlen {
		return s
	}
	bytes[maxlen-3], bytes[maxlen-2], bytes[maxlen-1] = '.', '.', '.'
	return string(bytes[:maxlen])
}

func (sim *Simulator) reset() {
	sim.pc = sim.entryPoint
	for i := range sim.registers {
		sim.registers[i] = 0
	}
	sim.memory = map[uint32][]byte{}

	sim.scrollViewInstruction(0)
	for i := range sim.view.Mems {
		sim.view.Mems[i].BaseAddress = fmt.Sprintf("0x%08x", uint32(i*16))
	}
	sim.syncView()
	sim.view.Step = false
	sim.view.Timeout = false

	sim.last = nil
}

func (sim *Simulator) scrollViewInstruction(current int) {
	const viewSize = len(sim.view.Codes)
	const middle = viewSize / 2
	if sim.instructionViewBase() <= current && current <= sim.instructionViewBase()+middle {
		return
	}

	base := current - middle
	instructionSize := len(sim.instructions)
	if instructionSize < current+middle {
		base = instructionSize - viewSize
	}
	base = max(base, 0)

	if sim.instructionViewBase() == base {
		return
	}

	for i, v := range sim.instructions[base : base+viewSize] {
		sim.view.Codes[i].Address = fmt.Sprintf("0x%08x", sim.entryPoint+uint32((base+i)*4))
		sim.view.Codes[i].Label = formatLabel(v.Label, sim.view.InstructionWidth)
		sim.view.Codes[i].Mnemonic = v.MnemonicRaw
		sim.view.Codes[i].Operand = formatOperand(v.Operand, sim.view.InstructionWidth)
	}
}

func (sim *Simulator) instructionViewBase() int {
	addr := sim.view.Codes[0].Address
	if addr == "" {
		return -1
	}
	base, _ := strconv.ParseUint(addr, 0, 32)
	return int(uint32(base)-sim.entryPoint) / 4
}

func (sim *Simulator) syncView() {
	sim.syncViewRegister()
	sim.syncViewMemory()
}

func (sim *Simulator) syncViewRegister() {
	for i, v := range sim.registers {
		sim.view.Regs[i].Signed = fmt.Sprintf("%d", int32(v))
		sim.view.Regs[i].Unsigned = fmt.Sprintf("%d", v)
		sim.view.Regs[i].Bin = fmt.Sprintf("%032b", v)
		sim.view.Regs[i].Hex = fmt.Sprintf("%08x", v)
	}
}

func (sim *Simulator) syncViewMemory() {
	// upper half
	base := sim.view.memoryBase()
	sim.readMemory(base) // if not exists, be generated
	mems := sim.memory[base]
	for i, v := range mems {
		ii, j := sim.view.memoryIndex(base + uint32(i))
		sim.view.Mems[ii].Bytes[j].Hex = fmt.Sprintf("%02x", v)
	}

	// lower half
	base += uint32(len(mems))
	sim.readMemory(base) // if not exists, be generated
	mems = sim.memory[base]
	for i, v := range mems {
		ii, j := sim.view.memoryIndex(base + uint32(i))
		sim.view.Mems[ii].Bytes[j].Hex = fmt.Sprintf("%02x", v)
	}
}

func (sim *Simulator) focusViewMemoryRange(effect *Effect) {
	either := effect.MemWrite
	if either == nil {
		either = effect.MemRead
	}
	if either == nil {
		return
	}

	base := sim.view.memoryBase()
	minAddr := slices.Min(either)
	maxAddr := slices.Max(either)
	if base <= minAddr && maxAddr < base+(16*16*2) {
		return
	}

	if maxAddr < 0x200 {
		base = 0
	} else {
		base = min(minAddr&0xffffff00, 0xfffffe00)
	}
	for i := range sim.view.Mems {
		sim.view.Mems[i].BaseAddress = fmt.Sprintf("0x%08x", base+uint32(i*16))
	}

	sim.syncViewMemory()
}

func (view *SinglePageView) memoryBase() uint32 {
	base, _ := strconv.ParseUint(view.Mems[0].BaseAddress, 0, 32)
	return uint32(base)
}

func (view *SinglePageView) memoryIndex(addr uint32) (i, j int) {
	base := view.memoryBase()
	if addr < base {
		return -1, -1
	}
	size := len(view.Mems[0].Bytes)
	i, j = int(addr-base)/size, int(addr-base)%size
	return
}

func (view *SinglePageView) setStatus(status DisabledButton) {
	view.Disabled = status
}

func (view *SinglePageView) wasDisabled(req string) bool {
	switch req {
	case RUN:
		return view.Disabled.Run
	case STEP:
		return view.Disabled.Step
	case STOP:
		return view.Disabled.Stop
	case RELOAD:
		return view.Disabled.Reload
	}
	return false
}

func (sim *Simulator) validate(lines [][3]string) bool {
	valid := true

	w := sim.validationError
	fileName := sim.fileName

	definedLabelMap := map[string]struct{}{}
	filtered := [][3]string{}
	for i, v := range lines {
		lineNo, definedLabel, mnemonic, operand := i+1, v[0], v[1], v[2]
		if definedLabel != "" {
			if !validateLabel(definedLabel) {
				valid = false
				logerr(w, fileName, lineNo, "invalid label(%s)", definedLabel)
			} else if _, ok := definedLabelMap[definedLabel]; ok {
				valid = false
				logerr(w, fileName, lineNo, "label duplicated(%s)", definedLabel)
			} else {
				definedLabelMap[definedLabel] = struct{}{} // case-sensitive
			}
		}
		if mnemonic != "" {
			filtered = append(filtered, [3]string{strconv.Itoa(lineNo), mnemonic, operand})
		}
	}

	for _, v := range filtered {
		lineNo, _ := strconv.Atoi(v[0])
		mnemonic, operand := v[1], v[2]
		switch strings.ToLower(mnemonic) { // case-insensitive
		case "add", "sub", "and", "or", "xor", "sll", "srl", "sra", "slt", "sltu":
			valid = validateR(w, fileName, lineNo, operand) && valid
		case "addi", "andi", "ori", "xori":
			valid = validateI(w, fileName, lineNo, operand) && valid
		case "sb", "sh", "sw":
			valid = validateS(w, fileName, lineNo, operand) && valid
		case "beq", "bne", "blt", "bltu", "bge", "bgeu":
			valid = validateB(w, fileName, lineNo, operand, definedLabelMap) && valid
		case "lui", "auipc":
			valid = validateU(w, fileName, lineNo, operand) && valid
		case "jal":
			valid = validateJ(w, fileName, lineNo, operand, definedLabelMap) && valid
		case "jalr":
			valid = validateJalr(w, fileName, lineNo, operand) && valid
		case "slli", "srli", "srai", "slti", "sltiu":
			valid = validateShift(w, fileName, lineNo, operand) && valid
		case "lbu", "lb", "lhu", "lh", "lw":
			valid = validateLoad(w, fileName, lineNo, operand) && valid
		case "ecall", "ebreak", "fence":
			valid = false
			logerr(w, fileName, lineNo, "unimplemented instruction(%s)", mnemonic)
		case "csrrw", "csrrs", "csrrc", "csrrwi", "csrrsi", "csrrci":
			// once it was RV32I, but was excluded in Ratified version. move to Zicsr. no longer RV32I Base Integer Instruction Set
			valid = false
			logerr(w, fileName, lineNo, "unimplemented Zicsr instruction(%s)", mnemonic)
		case "fence.i":
			// once it was RV32I, but was excluded in Ratified version. move to Zifencei. no longer RV32I Base Integer Instruction Set
			valid = false
			logerr(w, fileName, lineNo, "unimplemented Zifencei instruction(%s)", mnemonic)
		default:
			valid = false
			logerr(w, fileName, lineNo, "unknown instruction(%s)", mnemonic)
		}
	}

	return valid
}

// unspecified. avoid unlimited
func validateLabel(label string) bool {
	if len(label) < 2 || 4096 < len(label) {
		return false
	}
	bytes := []byte(label)
	if !validateLabelFirstChar(bytes[0]) {
		return false
	}
	if bytes[len(bytes)-1] != ':' { // trailing
		return false
	}
	for _, v := range bytes[1 : len(bytes)-1] {
		if !validateLabelChar(v) {
			return false
		}
	}
	return true
}

func validateLabelFirstChar(c byte) bool {
	return ('A' <= c && c <= 'Z') || ('a' <= c && c <= 'z') || c == '_' || c == '.' || c == '$'
}

func validateLabelChar(c byte) bool {
	return validateLabelFirstChar(c) || ('0' <= c && c <= '9')
}

func validateR(w io.Writer, fileName string, lineNo int, operand string) bool {
	const exp = 3
	operands := strings.SplitN(operand, ",", exp+1)
	if len(operands) != exp {
		logerr(w, fileName, lineNo, "parse failed")
		return false
	}
	valid := true
	rd, rs1, rs2 := strings.TrimSpace(operands[0]), strings.TrimSpace(operands[1]), strings.TrimSpace(operands[2])
	if _, ok := registerMapping[rd]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rd(%s)", rd)
	}
	if _, ok := registerMapping[rs1]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rs1(%s)", rs1)
	}
	if _, ok := registerMapping[rs2]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rs2(%s)", rs2)
	}
	return valid
}

func validateI(w io.Writer, fileName string, lineNo int, operand string) bool {
	return validateImmediate(w, fileName, lineNo, operand, false)
}

func validateS(w io.Writer, fileName string, lineNo int, operand string) bool {
	return validateOffset(w, fileName, lineNo, operand, true)
}

func validateB(w io.Writer, fileName string, lineNo int, operand string, definedLabelMap map[string]struct{}) bool {
	const exp = 3
	operands := strings.SplitN(operand, ",", exp+1)
	if len(operands) != exp {
		logerr(w, fileName, lineNo, "parse failed")
		return false
	}
	rs1, rs2, definedLabel := strings.TrimSpace(operands[0]), strings.TrimSpace(operands[1]), strings.TrimSpace(operands[2])+":"
	valid := true
	if _, ok := registerMapping[rs1]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rs1(%s)", rs1)
	}
	if _, ok := registerMapping[rs2]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rs2(%s)", rs2)
	}
	if _, ok := definedLabelMap[definedLabel]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "label not found(%s)", definedLabel)
	}
	// validation of the following specifications is unimplemented
	// The conditional branch range is plus-minus 4 KiB
	return valid
}

func validateU(w io.Writer, fileName string, lineNo int, operand string) bool {
	const exp = 2
	operands := strings.SplitN(operand, ",", exp+1)
	if len(operands) != exp {
		logerr(w, fileName, lineNo, "parse failed")
		return false
	}
	rd, imm := strings.TrimSpace(operands[0]), strings.TrimSpace(operands[1])
	valid := true
	if _, ok := registerMapping[rd]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rd(%s)", rd)
	}
	if _, err := strconv.ParseUint(imm, 0, 20); err != nil {
		valid = false
		logerr(w, fileName, lineNo, "invalid immediate(%s) 20 bit unsigned integer", imm)
	}
	return valid
}

func validateJ(w io.Writer, fileName string, lineNo int, operand string, definedLabelMap map[string]struct{}) bool {
	const exp = 2
	operands := strings.SplitN(operand, ",", exp+1)
	if len(operands) != exp {
		if len(operands) != 1 {
			logerr(w, fileName, lineNo, "parse failed")
			return false
		}
		operands = []string{"", operands[0]}
	}
	rd, definedLabel := strings.TrimSpace(operands[0]), strings.TrimSpace(operands[1])+":"
	if rd == "" {
		rd = ra
	}
	valid := true
	if _, ok := registerMapping[rd]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rd(%s)", rd)
	}
	if _, ok := definedLabelMap[definedLabel]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "label not found(%s)", definedLabel)
	}
	// validation of the following specifications is unimplemented
	// Jumps can therefore target a plus-minus 1 MiB range
	return valid
}

func validateJalr(w io.Writer, fileName string, lineNo int, operand string) bool {
	operands := strings.SplitN(operand, ",", 3)
	if len(operands) == 1 {
		operand = ra + "," + operand
	} else if len(operands) == 2 && strings.TrimSpace(operands[0]) == "" {
		operand = ra + operand
	}
	return validateOffset(w, fileName, lineNo, operand, false)
}

func validateShift(w io.Writer, fileName string, lineNo int, operand string) bool {
	return validateImmediate(w, fileName, lineNo, operand, true)
}

func validateLoad(w io.Writer, fileName string, lineNo int, operand string) bool {
	return validateOffset(w, fileName, lineNo, operand, false)
}

func validateImmediate(w io.Writer, fileName string, lineNo int, operand string, shift bool) bool {
	const exp = 3
	operands := strings.SplitN(operand, ",", exp+1)
	if len(operands) != exp {
		logerr(w, fileName, lineNo, "parse failed")
		return false
	}
	rd, rs1, imm := strings.TrimSpace(operands[0]), strings.TrimSpace(operands[1]), strings.TrimSpace(operands[2])
	valid := true
	if _, ok := registerMapping[rd]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rd(%s)", rd)
	}
	if _, ok := registerMapping[rs1]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rs1(%s)", rs1)
	}
	if shift {
		if shamt, err := strconv.ParseInt(imm, 0, 5+1); err != nil || (shamt < 0 || 31 < shamt) {
			valid = false
			logerr(w, fileName, lineNo, "invalid shamt(%s) 0 <= shamt <= 31", imm)
		}
	} else {
		if _, err := strconv.ParseInt(imm, 0, 12); err != nil {
			valid = false
			logerr(w, fileName, lineNo, "invalid immediate(%s) 12 bit signed integer", imm)
		}
	}
	return valid
}

func validateOffset(w io.Writer, fileName string, lineNo int, operand string, store bool) bool {
	const exp = 2
	operands := strings.SplitN(operand, ",", exp+1)
	if len(operands) != exp {
		logerr(w, fileName, lineNo, "parse failed")
		return false
	}
	b := strings.Index(operands[1], "(")
	e := strings.Index(operands[1], ")")
	if b <= 0 || e <= 0 || e < b || e != len(operands[1])-1 {
		logerr(w, fileName, lineNo, "parse failed(%s)", strings.TrimSpace(operands[1]))
		return false
	}
	rdrs2, offset, rs1 := strings.TrimSpace(operands[0]), strings.TrimSpace(operands[1][:b]), strings.TrimSpace(operands[1][b+1:e])
	valid := true
	if _, ok := registerMapping[rdrs2]; !ok {
		valid = false
		if store {
			logerr(w, fileName, lineNo, "invalid rs2(%s)", rdrs2)
		} else {
			logerr(w, fileName, lineNo, "invalid rd(%s)", rdrs2)
		}
	}
	if _, ok := registerMapping[rs1]; !ok {
		valid = false
		logerr(w, fileName, lineNo, "invalid rs1(%s)", rs1)
	}
	if offset != "" {
		if _, err := strconv.ParseInt(offset, 0, 12); err != nil {
			valid = false
			logerr(w, fileName, lineNo, "invalid offset(%s) 12 bit signed integer", offset)
		}
	}
	return valid
}

func logerr(w io.Writer, fileName string, lineNo int, format string, a ...any) {
	datetime := time.Now().Format(time.DateTime)
	prefix := fmt.Sprintf("%s %s:%d ", datetime, fileName, lineNo)
	fmt.Fprintln(w, prefix+fmt.Sprintf(format, a...))
}

func (sim *Simulator) executeCurrent() *Effect {
	current := sim.currentInstructionIndex()
	mnemonic, operand := sim.instructions[current].Mnemonic, sim.instructions[current].Operand // validated and normalized

	var imm, offset uint32 // sign-extended
	var shamt, readBytes, writeBytes int
	var addr uint32
	var target *uint32

	jump := false
	rd, rs1, rs2 := -1, -1, -1
	rdS, rsS := true, true // used by signed
	rdU, rsU := true, true // used by unsigned

	x := sim.registers // short name for read

	switch mnemonic {
	case "add":
		rd, rs1, rs2 = decodeR(operand)
		sim.registers[rd] = x[rs1] + x[rs2]
	case "addi":
		rd, rs1, imm = decodeI(operand)
		sim.registers[rd] = x[rs1] + imm
	case "sub":
		rd, rs1, rs2 = decodeR(operand)
		sim.registers[rd] = x[rs1] - x[rs2]
	case "and":
		rd, rs1, rs2 = decodeR(operand)
		sim.registers[rd] = x[rs1] & x[rs2]
	case "or":
		rd, rs1, rs2 = decodeR(operand)
		sim.registers[rd] = x[rs1] | x[rs2]
	case "xor":
		rd, rs1, rs2 = decodeR(operand)
		sim.registers[rd] = x[rs1] ^ x[rs2]
	case "andi":
		rd, rs1, imm = decodeI(operand)
		sim.registers[rd] = x[rs1] & imm
	case "ori":
		rd, rs1, imm = decodeI(operand)
		sim.registers[rd] = x[rs1] | imm
	case "xori":
		rd, rs1, imm = decodeI(operand)
		sim.registers[rd] = x[rs1] ^ imm
	case "sll":
		rd, rs1, rs2 = decodeR(operand)
		shamt = int(x[rs2] & 0b11111)       // lower 5 bits
		sim.registers[rd] = x[rs1] << shamt // logical left shift
	case "slli":
		rd, rs1, shamt = decodeShift(operand)
		sim.registers[rd] = x[rs1] << shamt // logical left shift
	case "srl":
		rd, rs1, rs2 = decodeR(operand)
		shamt = int(x[rs2] & 0b11111)       // lower 5 bits
		sim.registers[rd] = x[rs1] >> shamt // logical right shift
	case "srli":
		rd, rs1, shamt = decodeShift(operand)
		sim.registers[rd] = x[rs1] >> shamt // logical right shift
	case "sra":
		rd, rs1, rs2 = decodeR(operand)
		shamt = int(x[rs2] & 0b11111)                      // lower 5 bits
		sim.registers[rd] = uint32(int32(x[rs1]) >> shamt) // arithmetic right shift
	case "srai":
		rd, rs1, shamt = decodeShift(operand)
		sim.registers[rd] = uint32(int32(x[rs1]) >> shamt) // arithmetic right shift
	case "slt":
		rd, rs1, rs2 = decodeR(operand)
		if int32(x[rs1]) < int32(x[rs2]) {
			sim.registers[rd] = 1
		} else {
			sim.registers[rd] = 0
		}
		rsU = false
	case "sltu":
		rd, rs1, rs2 = decodeR(operand)
		if x[rs1] < x[rs2] {
			sim.registers[rd] = 1
		} else {
			sim.registers[rd] = 0
		}
		rsS = false
	case "slti":
		rd, rs1, imm = decodeI(operand)
		if int32(x[rs1]) < int32(imm) {
			sim.registers[rd] = 1
		} else {
			sim.registers[rd] = 0
		}
		rsU = false
	case "sltiu":
		rd, rs1, imm = decodeI(operand)
		if x[rs1] < imm {
			sim.registers[rd] = 1
		} else {
			sim.registers[rd] = 0
		}
		rsS = false
	case "lui":
		rd, imm = decodeU(operand)
		sim.registers[rd] = imm << 12 // filling in the lowest 12 bits with zeros
	case "auipc":
		rd, imm = decodeU(operand)
		offset = imm << 12 // filling in the lowest 12 bits with zeros
		sim.registers[rd] = sim.pc + offset
	case "lb":
		rd, rs1, offset = decodeOffset(operand)
		addr = x[rs1] + offset
		sim.registers[rd] = uint32(int8(sim.readMemory(addr))) // sign extends
		readBytes, rdU = memoryBytes(mnemonic), false
	case "lbu":
		rd, rs1, offset = decodeOffset(operand)
		addr = x[rs1] + offset
		sim.registers[rd] = uint32(sim.readMemory(addr)) // zero extends
		readBytes, rdS = memoryBytes(mnemonic), false
	case "lh":
		rd, rs1, offset = decodeOffset(operand)
		addr = x[rs1] + offset
		m := [...]byte{sim.readMemory(addr), sim.readMemory(addr + 1)}
		sim.registers[rd] = uint32(int16(uint16(m[0]) | (uint16(m[1]) << 8))) // sign extends. little-endian
		readBytes, rdU = memoryBytes(mnemonic), false
	case "lhu":
		rd, rs1, offset = decodeOffset(operand)
		addr = x[rs1] + offset
		m := [...]byte{sim.readMemory(addr), sim.readMemory(addr + 1)}
		sim.registers[rd] = uint32(m[0]) | (uint32(m[1]) << 8) // zero extends. little-endian
		readBytes, rdS = memoryBytes(mnemonic), false
	case "lw":
		rd, rs1, offset = decodeOffset(operand)
		addr = x[rs1] + offset
		m := [...]byte{sim.readMemory(addr), sim.readMemory(addr + 1), sim.readMemory(addr + 2), sim.readMemory(addr + 3)}
		sim.registers[rd] = uint32(m[0]) | (uint32(m[1]) << 8) | (uint32(m[2]) << 16) | (uint32(m[3]) << 24) // little-endian
		readBytes = memoryBytes(mnemonic)
	case "sb", "sh", "sw":
		rs2, rs1, offset = decodeOffset(operand)
		addr = x[rs1] + offset
		writeBytes = memoryBytes(mnemonic)
		val := x[rs2]
		for i := 0; i < writeBytes; i++ {
			sim.writeMemory(addr+uint32(i), byte(val>>(i*8))) // little-endian
		}
	case "jal":
		rd, addr = decodeJ(operand, sim.labelMapping)
		sim.registers[rd] = sim.pc + 4
		target = &addr
		jump = true
	case "jalr":
		rd, rs1, offset = decodeJalr(operand)
		sim.registers[rd] = sim.pc + 4
		addr = x[rs1] + offset
		target = &addr
		jump = true
	case "beq":
		rs1, rs2, addr = decodeB(operand, sim.labelMapping)
		target = &addr
		jump = x[rs1] == x[rs2]
	case "bne":
		rs1, rs2, addr = decodeB(operand, sim.labelMapping)
		target = &addr
		jump = x[rs1] != x[rs2]
	case "blt":
		rs1, rs2, addr = decodeB(operand, sim.labelMapping)
		target = &addr
		jump = int32(x[rs1]) < int32(x[rs2])
		rsU = false
	case "bltu":
		rs1, rs2, addr = decodeB(operand, sim.labelMapping)
		target = &addr
		jump = x[rs1] < x[rs2]
		rsS = false
	case "bge":
		rs1, rs2, addr = decodeB(operand, sim.labelMapping)
		target = &addr
		jump = int32(x[rs1]) >= int32(x[rs2])
		rsU = false
	case "bgeu":
		rs1, rs2, addr = decodeB(operand, sim.labelMapping)
		target = &addr
		jump = x[rs1] >= x[rs2]
		rsS = false
	}

	if rd == 0 {
		sim.registers[0] = 0 // restore hardwired value
	}

	if jump {
		sim.pc = *target
	} else {
		sim.pc += 4
	}

	return &Effect{
		Current:  current,
		Ref:      sim.instructionIndex(target),
		Jump:     jump,
		Rd:       rd,
		Rs1:      rs1,
		Rs2:      rs2,
		RdS:      rdS,
		RdU:      rdU,
		RsS:      rsS,
		RsU:      rsU,
		MemRead:  addresses(addr, readBytes),
		MemWrite: addresses(addr, writeBytes),
	}
}

func decodeR(operand string) (rd, rs1, rs2 int) {
	operands := strings.SplitN(operand, ",", 3)
	rd = registerMapping[operands[0]]
	rs1 = registerMapping[operands[1]]
	rs2 = registerMapping[operands[2]]
	return
}

func decodeI(operand string) (rd, rs1 int, imm uint32) {
	operands := strings.SplitN(operand, ",", 3)
	rd = registerMapping[operands[0]]
	rs1 = registerMapping[operands[1]]
	imm = parseIimmediate(operands[2])
	return
}

func decodeB(operand string, labelMapping map[string]uint32) (rs1, rs2 int, addr uint32) {
	operands := strings.SplitN(operand, ",", 3)
	rs1 = registerMapping[operands[0]]
	rs2 = registerMapping[operands[1]]
	label := operands[2]
	addr = labelMapping[label]
	return
}

func decodeU(operand string) (rd int, imm uint32) {
	operands := strings.SplitN(operand, ",", 2)
	rd = registerMapping[operands[0]]
	imm = parseUimmediate(operands[1])
	return
}

func decodeJ(operand string, labelMapping map[string]uint32) (rd int, addr uint32) {
	operands := strings.SplitN(operand, ",", 2)
	if len(operands) == 1 {
		operands = []string{ra, operands[0]}
	} else if operands[0] == "" {
		operands = []string{ra, operands[1]}
	}
	rd = registerMapping[operands[0]]
	label := operands[1]
	addr = labelMapping[label]
	return
}

func decodeJalr(operand string) (rd, rs1 int, offset uint32) {
	if strings.Index(operand, ",") == -1 {
		operand = ra + "," + operand
	} else if operand[0] == ',' {
		operand = ra + operand
	}
	return decodeOffset(operand)
}

func decodeShift(operand string) (rd, rs1, shamt int) {
	operands := strings.SplitN(operand, ",", 3)
	rd = registerMapping[operands[0]]
	rs1 = registerMapping[operands[1]]
	shamt = parseShamt(operands[2])
	return
}

func decodeOffset(operand string) (rdrs2, rs1 int, offset uint32) {
	operands := strings.SplitN(operand, ",", 2)
	rdrs2 = registerMapping[operands[0]]
	b := strings.Index(operands[1], "(")
	e := strings.Index(operands[1], ")")
	if operands[1][0] == '(' {
		offset = 0
	} else {
		offset = parseIimmediate(operands[1][:b])
	}
	rs1 = registerMapping[operands[1][b+1:e]]
	return
}

func parseIimmediate(s string) uint32 {
	i, _ := strconv.ParseInt(s, 0, 12)
	return uint32(int32(i)) // sign extended
}

func parseUimmediate(s string) uint32 {
	i, _ := strconv.ParseUint(s, 0, 20)
	return uint32(i)
}

func parseShamt(s string) int {
	i, _ := strconv.ParseUint(s, 0, 5)
	return int(i) // lower 5 bits
}

func memoryBytes(mnemonic string) int {
	// single byte addressable
	switch mnemonic[1] {
	case 'w': // word(32 bits)
		return 4 // 4 bytes
	case 'h': // halfword(16 bits)
		return 2 // 2 bytes
	case 'b': // byte(8 bits)
		return 1 // 1 byte
	}
	return 0 // unused. resolved missing return
}

func addresses(addr uint32, bytes int) []uint32 {
	if bytes <= 0 {
		return nil
	}
	addrs := []uint32{}
	for i := uint32(0); i < uint32(bytes); i++ {
		addrs = append(addrs, addr+i)
	}
	return addrs
}

const simulatorHTML = `
<!DOCTYPE html>
<html>
<head>
<style>
table {
	font-family: monospace, 'Courier New';
	border-top: 2px solid;
	border-bottom: 2px solid;
}
th {
	font-weight: normal;
	color: darkgray;
}
td {
	color: dimgray;
}
</style>
</head>
<body>
<h1>RISC-V Reduced Visual Simulator</h1>
<table cellspacing=0 style='float:left;border-left:2px solid;border-right:2px solid'>
<thead>
<tr>{{range .InstructionWidth}}<th style='color:transparent;font-weight:bold'>{{.}}</th>{{end}}</tr>
<tr><th style='color:black'>Address</th><th style='color:black'>Label</th><th colspan=2 style='color:black'>Instruction</th></tr>
<tr><td colspam=4>&nbsp;</td></tr>
</thead>
<tbody>
{{- range .Codes}}
<tr>
{{- if .RefColor}}
<th style='text-align:center;color:{{.RefColor}}'>{{.Address}}</th>
<td style='color:{{.RefColor}}'>{{.Label}}</td>
{{- else}}
<th style='text-align:center'>{{.Address}}</th>
<td>{{.Label}}</td>
{{- end}}
{{- if .Current}}
<td style='color:#011e41;background-color:#fdda64'>{{.Mnemonic}}</td>
<td style='color:#011e41;background-color:#fdda64'>{{.Operand}}</td>
{{- else}}
<td style='color:#003262;'>{{.Mnemonic}}</td>
<td style='color:#003262;'>{{.Operand}}</td>
{{- end}}
</tr>
{{- end}}
</tbody>
<tfooter><tr><td colspam=4>&nbsp;</td></tr></tfooter>
</table>
<table cellspacing=0 style='float:left;border-right:2px solid'>
<thead>
<tr>{{range .RegisterWidth}}<th style='color:transparent;font-weight:bold'>{{.}}</th>{{end}}</tr>
<tr><th colspan=4 style='color:black'>Register</th><th style='color:black'>Signed</th><th style='color:black'>Unsigned</th><th style='color:black'>Bin</th><th style='color:black'>Hex</th></tr>
<tr><td colspam=8>&nbsp;</td></tr>
</thead>
<tbody>
{{- range .Regs}}
{{- if .Even}}
<tr>
{{- else}}
<tr style='background-color:whitesmoke'>
{{- end}}
<th style='color:#011e41'>{{.Name}}</th>
<th>(</th><th style='color:#011e41;text-align:center'>{{.ABI}}</th><th>)</th>
{{- if .Color}}
{{- if .SignedUnused}}
<td style='text-align:right;text-decoration: line-through double {{.Color}};color:{{.Color}}'>{{.Signed}}</td>
{{- else}}
<td style='text-align:right;font-weight:bold;color:{{.Color}}'>{{.Signed}}</td>
{{- end}}
{{- if .UnsignedUnused}}
<td style='text-align:right;text-decoration: line-through double {{.Color}};color:{{.Color}}'>{{.Unsigned}}</td>
{{- else}}
<td style='text-align:right;font-weight:bold;color:{{.Color}}'>{{.Unsigned}}</td>
{{- end}}
<td style='text-align:right;color:{{.Color}}'>{{.Bin}}</td>
<td style='text-align:center;color:{{.Color}}'>{{.Hex}}</td>
{{- else}}
<td style='text-align:right'>{{.Signed}}</td>
<td style='text-align:right'>{{.Unsigned}}</td>
<td style='text-align:right'>{{.Bin}}</td>
<td style='text-align:center'>{{.Hex}}</td>
{{- end}}
</tr>
{{- end}}
</tbody>
<tfooter><tr><td colspam=8>&nbsp;</td></tr></tfooter>
</table>
<table cellspacing=0 style='border-right:2px solid'>
<thead>
<tr>{{range .MemoryWidth}}<th style='color:transparent;font-weight:normal'>{{.}}</th>{{end}}</tr>
<tr><th style='color:black'>Address</th>{{range .MemoryOffset}}<th>{{.}}</th>{{end}}</tr>
<tr><td colspam=17>&nbsp;</td></tr>
</thead>
<tbody>
{{- range .Mems}}
<tr>
<th style='text-align:center'>{{.BaseAddress}}</th>
{{- range .Bytes}}
{{- if .Color}}
<td style='text-align:center;font-weight:bold;color:{{.Color}}'>{{.Hex}}</td>
{{- else}}
<td style='text-align:center'>{{.Hex}}</td>
{{- end}}
{{- end}}
</tr>
{{- end}}
</tbody>
<tfooter><tr><td colspam=17>&nbsp;</td></tr></tfooter>
</table>
<br>
<form method=POST>
<input type=submit name='button' value='RUN'{{if .Disabled.Run}} disabled {{end}}>&nbsp;
<input type=submit name='button' value='STEP'{{if .Disabled.Step}} disabled {{end}}{{if .Step}} autofocus {{end}}>&nbsp;
<input type=submit name='button' value='STOP'{{if .Disabled.Stop}} disabled {{end}}>&nbsp;
<input type=submit name='button' value='RELOAD'{{if .Disabled.Reload}} disabled {{end}}>
</form>
{{- if .Failed}}
<p style='color:red'>failed. for more information, stderr</p>
{{- end}}
{{- if .Timeout}}
<p style='color:red'>timeout. if continue, RUN again</p>
{{- end}}
</body>
</html>
`
