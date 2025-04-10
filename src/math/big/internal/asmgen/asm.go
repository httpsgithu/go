// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package asmgen

import (
	"bytes"
	"cmp"
	"fmt"
	"math/bits"
	"slices"
	"strings"
)

// Note: Exported fields and methods are expected to be used
// by function generators (like the ones in add.go and so on).
// Unexported fields and methods should not be.

// An Asm is an assembly file being written.
type Asm struct {
	Arch     *Arch           // architecture
	out      bytes.Buffer    // output buffer
	regavail uint64          // bitmap of available registers
	enabled  map[Option]bool // enabled optional CPU features
}

// NewAsm returns a new Asm preparing assembly
// for the given architecture to be written to file.
func NewAsm(arch *Arch) *Asm {
	a := &Asm{Arch: arch, enabled: make(map[Option]bool)}
	buildTag := ""
	if arch.Build != "" {
		buildTag = " && (" + arch.Build + ")"
	}
	a.Printf(asmHeader, buildTag)
	return a
}

// Note: Using Copyright 2025, not the current year, to avoid test failures
// on January 1 and spurious diffs when regenerating assembly.
// The generator was written in 2025; that's good enough.
// (As a matter of policy the Go project does not update copyright
// notices every year, since copyright terms are so long anyway.)

var asmHeader = `// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Code generated by 'go generate' (with ./internal/asmgen). DO NOT EDIT.

//go:build !math_big_pure_go%s

#include "textflag.h"
`

// Fatalf reports a fatal error by panicking.
// Panicking is appropriate because there is a bug in the generator,
// and panicking will show the exact source lines leading to that bug.
func (a *Asm) Fatalf(format string, args ...any) {
	text := a.out.String()
	i := strings.LastIndex(text, "\nTEXT")
	text = text[i+1:]
	panic("[" + a.Arch.Name + "] asmgen internal error: " + fmt.Sprintf(format, args...) + "\n" + text)
}

// hint returns the register name for the given hint.
func (a *Asm) hint(h Hint) string {
	if h == HintCarry && a.Arch.regCarry != "" {
		return a.Arch.regCarry
	}
	if h == HintAltCarry && a.Arch.regAltCarry != "" {
		return a.Arch.regAltCarry
	}
	if h == HintNone || a.Arch.hint == nil {
		return ""
	}
	return a.Arch.hint(a, h)
}

// ZR returns the zero register (the specific register guaranteed to hold the integer 0),
// or else the zero Reg (Reg{}, which has r.Valid() == false).
func (a *Asm) ZR() Reg {
	return Reg{a.Arch.reg0}
}

// tmp returns the temporary register, or else the zero Reg.
// The temporary register is one available for use implementing logical instructions
// that compile into multiple actual instructions on a given system.
// The assembler sometimes uses it for that purpose, as do we.
// Of course, if we are using it, we'd better not emit an instruction that
// will cause the assembler to smash it while we want it to be holding
// a live value. In general it is the architecture implementation's responsibility
// not to suggest the use of any such pseudo-instructions in situations
// where they would cause problems.
func (a *Asm) tmp() Reg {
	return Reg{a.Arch.regTmp}
}

// Carry returns the carry register, or else the zero Reg.
func (a *Asm) Carry() Reg {
	return Reg{a.Arch.regCarry}
}

// AltCarry returns the secondary carry register, or else the zero Reg.
func (a *Asm) AltCarry() Reg {
	return Reg{a.Arch.regAltCarry}
}

// Imm returns a Reg representing an immediate (constant) value.
func (a *Asm) Imm(x int) Reg {
	if x == 0 && a.Arch.reg0 != "" {
		return Reg{a.Arch.reg0}
	}
	return Reg{fmt.Sprintf("$%d", x)}
}

// IsZero reports whether r is a zero immediate or the zero register.
func (a *Asm) IsZero(r Reg) bool {
	return r.name == "$0" || a.Arch.reg0 != "" && r.name == a.Arch.reg0
}

// Reg allocates a new register.
func (a *Asm) Reg() Reg {
	i := bits.TrailingZeros64(a.regavail)
	if i == 64 {
		a.Fatalf("out of registers")
	}
	a.regavail ^= 1 << i
	return Reg{a.Arch.regs[i]}
}

// RegHint allocates a new register, with a hint as to its purpose.
func (a *Asm) RegHint(hint Hint) Reg {
	if name := a.hint(hint); name != "" {
		i := slices.Index(a.Arch.regs, name)
		if i < 0 {
			return Reg{name}
		}
		if a.regavail&(1<<i) == 0 {
			a.Fatalf("hint for already allocated register %s", name)
		}
		a.regavail &^= 1 << i
		return Reg{name}
	}
	return a.Reg()
}

// Free frees a previously allocated register.
// If r is not a register (if it's an immediate or a memory reference), Free is a no-op.
func (a *Asm) Free(r Reg) {
	i := slices.Index(a.Arch.regs, r.name)
	if i < 0 {
		return
	}
	if a.regavail&(1<<i) != 0 {
		a.Fatalf("register %s already freed", r.name)
	}
	a.regavail |= 1 << i
}

// Unfree reallocates a previously freed register r.
// If r is not a register (if it's an immediate or a memory reference), Unfree is a no-op.
// If r is not free for allocation, Unfree panics.
// A Free paired with Unfree can release a register for use temporarily
// but then reclaim it, such as at the end of a loop body when it must be restored.
func (a *Asm) Unfree(r Reg) {
	i := slices.Index(a.Arch.regs, r.name)
	if i < 0 {
		return
	}
	if a.regavail&(1<<i) == 0 {
		a.Fatalf("register %s not free", r.name)
	}
	a.regavail &^= 1 << i
}

// A RegsUsed is a snapshot of which registers are allocated.
type RegsUsed struct {
	avail uint64
}

// RegsUsed returns a snapshot of which registers are currently allocated,
// which can be passed to a future call to [Asm.SetRegsUsed].
func (a *Asm) RegsUsed() RegsUsed {
	return RegsUsed{a.regavail}
}

// SetRegsUsed sets which registers are currently allocated.
// The argument should have been returned from a previous
// call to [Asm.RegsUsed].
func (a *Asm) SetRegsUsed(used RegsUsed) {
	a.regavail = used.avail
}

// FreeAll frees all known registers.
func (a *Asm) FreeAll() {
	a.regavail = 1<<len(a.Arch.regs) - 1
}

// Printf emits to the assembly output.
func (a *Asm) Printf(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if strings.Contains(text, "%!") {
		a.Fatalf("printf error: %s", text)
	}
	a.out.WriteString(text)
}

// Comment emits a line comment to the assembly output.
func (a *Asm) Comment(format string, args ...any) {
	fmt.Fprintf(&a.out, "\t// %s\n", fmt.Sprintf(format, args...))
}

// EOL appends an end-of-line comment to the previous line.
func (a *Asm) EOL(format string, args ...any) {
	bytes := a.out.Bytes()
	if len(bytes) > 0 && bytes[len(bytes)-1] == '\n' {
		a.out.Truncate(a.out.Len() - 1)
	}
	a.Comment(format, args...)
}

// JmpEnable emits a test for the optional CPU feature that jumps to label if the feature is present.
// If JmpEnable returns false, the feature is not available on this architecture and no code was emitted.
func (a *Asm) JmpEnable(option Option, label string) bool {
	jmpEnable := a.Arch.options[option]
	if jmpEnable == nil {
		return false
	}
	jmpEnable(a, label)
	return true
}

// Enabled reports whether the optional CPU feature is considered
// to be enabled at this point in the assembly output.
func (a *Asm) Enabled(option Option) bool {
	return a.enabled[option]
}

// SetOption changes whether the optional CPU feature should be
// considered to be enabled.
func (a *Asm) SetOption(option Option, on bool) {
	a.enabled[option] = on
}

// op3 emits a 3-operand instruction op src1, src2, dst,
// taking care to handle 2-operand machines and also
// to simplify the printout when src2==dst.
func (a *Asm) op3(op string, src1, src2, dst Reg) {
	if op == "" {
		a.Fatalf("missing instruction")
	}
	if src2 == dst {
		// src2 and dst are same; print as 2-op form.
		a.Printf("\t%s %s, %s\n", op, src1, dst)
	} else if a.Arch.op3 != nil && !a.Arch.op3(op) {
		// Machine does not have 3-op form for op; convert to 2-op.
		if src1 == dst {
			a.Fatalf("implicit mov %s, %s would smash src1", src2, dst)
		}
		a.Mov(src2, dst)
		a.Printf("\t%s %s, %s\n", op, src1, dst)
	} else {
		// Full 3-op form.
		a.Printf("\t%s %s, %s, %s\n", op, src1, src2, dst)
	}
}

// Mov emits dst = src.
func (a *Asm) Mov(src, dst Reg) {
	if src != dst {
		a.Printf("\t%s %s, %s\n", a.Arch.mov, src, dst)
	}
}

// AddWords emits dst = src1*WordBytes + src2.
// It does not set or use the carry flag.
func (a *Asm) AddWords(src1 Reg, src2, dst RegPtr) {
	if a.Arch.addWords == "" {
		// Note: Assuming that Lsh does not clobber the carry flag.
		// Architectures where this is not true (x86) need to provide Arch.addWords.
		t := a.Reg()
		a.Lsh(a.Imm(bits.TrailingZeros(uint(a.Arch.WordBytes))), src1, t)
		a.Add(t, Reg(src2), Reg(dst), KeepCarry)
		a.Free(t)
		return
	}
	a.Printf("\t"+a.Arch.addWords+"\n", src1, src2, dst)
}

// And emits dst = src1 & src2
// It may modify the carry flag.
func (a *Asm) And(src1, src2, dst Reg) {
	a.op3(a.Arch.and, src1, src2, dst)
}

// Or emits dst = src1 | src2
// It may modify the carry flag.
func (a *Asm) Or(src1, src2, dst Reg) {
	a.op3(a.Arch.or, src1, src2, dst)
}

// Xor emits dst = src1 ^ src2
// It may modify the carry flag.
func (a *Asm) Xor(src1, src2, dst Reg) {
	a.op3(a.Arch.xor, src1, src2, dst)
}

// Neg emits dst = -src.
// It may modify the carry flag.
func (a *Asm) Neg(src, dst Reg) {
	if a.Arch.neg == "" {
		if a.Arch.rsb != "" {
			a.Printf("\t%s $0, %s, %s\n", a.Arch.rsb, src, dst)
			return
		}
		if a.Arch.sub != "" && a.Arch.reg0 != "" {
			a.Printf("\t%s %s, %s, %s\n", a.Arch.sub, src, a.Arch.reg0, dst)
			return
		}
		a.Fatalf("missing neg")
	}
	if src == dst {
		a.Printf("\t%s %s\n", a.Arch.neg, dst)
	} else {
		a.Printf("\t%s %s, %s\n", a.Arch.neg, src, dst)
	}
}

// HasRegShift reports whether the architecture can use shift expressions as operands.
func (a *Asm) HasRegShift() bool {
	return a.Arch.regShift
}

// LshReg returns a shift-expression operand src<<shift.
// If a.HasRegShift() == false, LshReg panics.
func (a *Asm) LshReg(shift, src Reg) Reg {
	if !a.HasRegShift() {
		a.Fatalf("no reg shift")
	}
	return Reg{fmt.Sprintf("%s<<%s", src, strings.TrimPrefix(shift.name, "$"))}
}

// Lsh emits dst = src << shift.
// It may modify the carry flag.
func (a *Asm) Lsh(shift, src, dst Reg) {
	if need := a.hint(HintShiftCount); need != "" && shift.name != need && !shift.IsImm() {
		a.Fatalf("shift count not in %s", need)
	}
	if a.HasRegShift() {
		a.Mov(a.LshReg(shift, src), dst)
		return
	}
	a.op3(a.Arch.lsh, shift, src, dst)
}

// LshWide emits dst = src << shift with low bits shifted from adj.
// It may modify the carry flag.
func (a *Asm) LshWide(shift, adj, src, dst Reg) {
	if a.Arch.lshd == "" {
		a.Fatalf("no lshwide on %s", a.Arch.Name)
	}
	if need := a.hint(HintShiftCount); need != "" && shift.name != need && !shift.IsImm() {
		a.Fatalf("shift count not in %s", need)
	}
	a.op3(fmt.Sprintf("%s %s,", a.Arch.lshd, shift), adj, src, dst)
}

// RshReg returns a shift-expression operand src>>shift.
// If a.HasRegShift() == false, RshReg panics.
func (a *Asm) RshReg(shift, src Reg) Reg {
	if !a.HasRegShift() {
		a.Fatalf("no reg shift")
	}
	return Reg{fmt.Sprintf("%s>>%s", src, strings.TrimPrefix(shift.name, "$"))}
}

// Rsh emits dst = src >> shift.
// It may modify the carry flag.
func (a *Asm) Rsh(shift, src, dst Reg) {
	if need := a.hint(HintShiftCount); need != "" && shift.name != need && !shift.IsImm() {
		a.Fatalf("shift count not in %s", need)
	}
	if a.HasRegShift() {
		a.Mov(a.RshReg(shift, src), dst)
		return
	}
	a.op3(a.Arch.rsh, shift, src, dst)
}

// RshWide emits dst = src >> shift with high bits shifted from adj.
// It may modify the carry flag.
func (a *Asm) RshWide(shift, adj, src, dst Reg) {
	if a.Arch.lshd == "" {
		a.Fatalf("no rshwide on %s", a.Arch.Name)
	}
	if need := a.hint(HintShiftCount); need != "" && shift.name != need && !shift.IsImm() {
		a.Fatalf("shift count not in %s", need)
	}
	a.op3(fmt.Sprintf("%s %s,", a.Arch.rshd, shift), adj, src, dst)
}

// SLTU emits dst = src2 < src1 (0 or 1), using an unsigned comparison.
func (a *Asm) SLTU(src1, src2, dst Reg) {
	switch {
	default:
		a.Fatalf("arch has no sltu/sgtu")
	case a.Arch.sltu != "":
		a.Printf("\t%s %s, %s, %s\n", a.Arch.sltu, src1, src2, dst)
	case a.Arch.sgtu != "":
		a.Printf("\t%s %s, %s, %s\n", a.Arch.sgtu, src2, src1, dst)
	}
}

// Add emits dst = src1+src2, with the specified carry behavior.
func (a *Asm) Add(src1, src2, dst Reg, carry Carry) {
	switch {
	default:
		a.Fatalf("unsupported carry behavior")
	case a.Arch.addF != nil && a.Arch.addF(a, src1, src2, dst, carry):
		// handled
	case a.Arch.add != "" && (carry == KeepCarry || carry == SmashCarry):
		a.op3(a.Arch.add, src1, src2, dst)
	case a.Arch.adds != "" && (carry == SetCarry || carry == SmashCarry):
		a.op3(a.Arch.adds, src1, src2, dst)
	case a.Arch.adc != "" && (carry == UseCarry || carry == UseCarry|SmashCarry):
		a.op3(a.Arch.adc, src1, src2, dst)
	case a.Arch.adcs != "" && (carry == UseCarry|SetCarry || carry == UseCarry|SmashCarry):
		a.op3(a.Arch.adcs, src1, src2, dst)
	case a.Arch.lea != "" && (carry == KeepCarry || carry == SmashCarry):
		if src1.IsImm() {
			a.Printf("\t%s %s(%s), %s\n", a.Arch.lea, src1.name[1:], src2, dst) // name[1:] removes $
		} else {
			a.Printf("\t%s (%s)(%s), %s\n", a.Arch.lea, src1, src2, dst)
		}
		if src2 == dst {
			a.EOL("ADD %s, %s", src1, dst)
		} else {
			a.EOL("ADD %s, %s, %s", src1, src2, dst)
		}

	case a.Arch.add != "" && a.Arch.regCarry != "":
		// Machine has no carry flag; instead we've dedicated a register
		// and use SLTU/SGTU (set less-than/greater-than unsigned)
		// to compute the carry flags as needed.
		// For ADD x, y, z, SLTU x/y, z, c computes the carry (borrow) bit.
		// Either of x or y can be used as the second argument, provided
		// it is not aliased to z.
		// To make the output less of a wall of instructions,
		// we comment the “higher-level” operation, with ... marking
		// continued instructions implementing the operation.
		cr := a.Carry()
		if carry&AltCarry != 0 {
			cr = a.AltCarry()
			if !cr.Valid() {
				a.Fatalf("alt carry not supported")
			}
			carry &^= AltCarry
		}
		tmp := a.tmp()
		if !tmp.Valid() {
			a.Fatalf("cannot simulate sub carry without regTmp")
		}
		switch carry {
		default:
			a.Fatalf("unsupported carry behavior")
		case UseCarry, UseCarry | SmashCarry:
			// Easy case, just add the carry afterward.
			if a.IsZero(src1) {
				// Only here to use the carry.
				a.Add(cr, src2, dst, KeepCarry)
				a.EOL("ADC $0, %s, %s", src2, dst)
				break
			}
			a.Add(src1, src2, dst, KeepCarry)
			a.EOL("ADC %s, %s, %s (cr=%s)", src1, src2, dst, cr)
			a.Add(cr, dst, dst, KeepCarry)
			a.EOL("...")

		case SetCarry:
			if a.IsZero(src1) && src2 == dst {
				// Only here to clear the carry flag. (Caller will comment.)
				a.Xor(cr, cr, cr)
				break
			}
			var old Reg // old is a src distinct from dst
			switch {
			case dst != src1:
				old = src1
			case dst != src2:
				old = src2
			default:
				// src1 == src2 == dst.
				// Overflows if and only if the high bit is set, so copy high bit to carry.
				a.Rsh(a.Imm(a.Arch.WordBits-1), src1, cr)
				a.EOL("ADDS %s, %s, %s (cr=%s)", src1, src2, dst, cr)
				a.Add(src1, src2, dst, KeepCarry)
				a.EOL("...")
				return
			}
			a.Add(src1, src2, dst, KeepCarry)
			a.EOL("ADDS %s, %s, %s (cr=%s)", src1, src2, dst, cr)
			a.SLTU(old, dst, cr) // dst < old (one of the src) implies carry
			a.EOL("...")

		case UseCarry | SetCarry:
			if a.IsZero(src1) {
				// Only here to use and then set the carry.
				// Easy since carry is not aliased to dst.
				a.Add(cr, src2, dst, KeepCarry)
				a.EOL("ADCS $0, %s, %s (cr=%s)", src2, dst, cr)
				a.SLTU(cr, dst, cr) // dst < cr implies carry
				a.EOL("...")
				break
			}
			// General case. Need to do two different adds (src1 + src2 + cr),
			// computing carry bits for both, and add'ing them together.
			// Start with src1+src2.
			var old Reg // old is a src distinct from dst
			switch {
			case dst != src1:
				old = src1
			case dst != src2:
				old = src2
			}
			if old.Valid() {
				a.Add(src1, src2, dst, KeepCarry)
				a.EOL("ADCS %s, %s, %s (cr=%s)", src1, src2, dst, cr)
				a.SLTU(old, dst, tmp) // // dst < old (one of the src) implies carry
				a.EOL("...")
			} else {
				// src1 == src2 == dst, like above. Sign bit is carry bit,
				// but we copy it into tmp, not cr.
				a.Rsh(a.Imm(a.Arch.WordBits-1), src1, tmp)
				a.EOL("ADCS %s, %s, %s (cr=%s)", src1, src2, dst, cr)
				a.Add(src1, src2, dst, KeepCarry)
				a.EOL("...")
			}
			// Add cr to dst.
			a.Add(cr, dst, dst, KeepCarry)
			a.EOL("...")
			a.SLTU(cr, dst, cr) // sum < cr implies carry
			a.EOL("...")
			// Add the two carry bits (at most one can be set, because (2⁶⁴-1)+(2⁶⁴-1)+1 < 2·2⁶⁴).
			a.Add(tmp, cr, cr, KeepCarry)
			a.EOL("...")
		}
	}
}

// Sub emits dst = src2-src1, with the specified carry behavior.
func (a *Asm) Sub(src1, src2, dst Reg, carry Carry) {
	switch {
	default:
		a.Fatalf("unsupported carry behavior")
	case a.Arch.subF != nil && a.Arch.subF(a, src1, src2, dst, carry):
		// handled
	case a.Arch.sub != "" && (carry == KeepCarry || carry == SmashCarry):
		a.op3(a.Arch.sub, src1, src2, dst)
	case a.Arch.subs != "" && (carry == SetCarry || carry == SmashCarry):
		a.op3(a.Arch.subs, src1, src2, dst)
	case a.Arch.sbc != "" && (carry == UseCarry || carry == UseCarry|SmashCarry):
		a.op3(a.Arch.sbc, src1, src2, dst)
	case a.Arch.sbcs != "" && (carry == UseCarry|SetCarry || carry == UseCarry|SmashCarry):
		a.op3(a.Arch.sbcs, src1, src2, dst)
	case strings.HasPrefix(src1.name, "$") && (carry == KeepCarry || carry == SmashCarry):
		// Running out of options; if this is an immediate
		// and we don't need to worry about carry semantics,
		// try adding the negation.
		if strings.HasPrefix(src1.name, "$-") {
			src1.name = "$" + src1.name[2:]
		} else {
			src1.name = "$-" + src1.name[1:]
		}
		a.Add(src1, src2, dst, carry)

	case a.Arch.sub != "" && a.Arch.regCarry != "":
		// Machine has no carry flag; instead we've dedicated a register
		// and use SLTU/SGTU (set less-than/greater-than unsigned)
		// to compute the carry bits as needed.
		// For SUB x, y, z, SLTU x, y, c computes the carry (borrow) bit.
		// To make the output less of a wall of instructions,
		// we comment the “higher-level” operation, with ... marking
		// continued instructions implementing the operation.
		// Be careful! Subtract and add have different overflow behaviors,
		// so the details here are NOT the same as in Add above.
		cr := a.Carry()
		if carry&AltCarry != 0 {
			a.Fatalf("alt carry not supported")
		}
		tmp := a.tmp()
		if !tmp.Valid() {
			a.Fatalf("cannot simulate carry without regTmp")
		}
		switch carry {
		default:
			a.Fatalf("unsupported carry behavior")
		case UseCarry, UseCarry | SmashCarry:
			// Easy case, just subtract the carry afterward.
			if a.IsZero(src1) {
				// Only here to use the carry.
				a.Sub(cr, src2, dst, KeepCarry)
				a.EOL("SBC $0, %s, %s", src2, dst)
				break
			}
			a.Sub(src1, src2, dst, KeepCarry)
			a.EOL("SBC %s, %s, %s", src1, src2, dst)
			a.Sub(cr, dst, dst, KeepCarry)
			a.EOL("...")

		case SetCarry:
			if a.IsZero(src1) && src2 == dst {
				// Only here to clear the carry flag.
				a.Xor(cr, cr, cr)
				break
			}
			// Compute the new carry first, in case dst is src1 or src2.
			a.SLTU(src1, src2, cr)
			a.EOL("SUBS %s, %s, %s", src1, src2, dst)
			a.Sub(src1, src2, dst, KeepCarry)
			a.EOL("...")

		case UseCarry | SetCarry:
			if a.IsZero(src1) {
				// Only here to use and then set the carry.
				if src2 == dst {
					// Unfortunate case. Using src2==dst is common (think x -= y)
					// and also more efficient on two-operand machines (like x86),
					// but here subtracting from dst will smash src2, making it
					// impossible to recover the carry information after the SUB.
					// But we want to use the carry, so we can't compute it before
					// the SUB either. Compute into a temporary and MOV.
					a.SLTU(cr, src2, tmp)
					a.EOL("SBCS $0, %s, %s", src2, dst)
					a.Sub(cr, src2, dst, KeepCarry)
					a.EOL("...")
					a.Mov(tmp, cr)
					a.EOL("...")
					break
				}
				a.Sub(cr, src2, dst, KeepCarry) // src2 not dst, so src2 preserved
				a.SLTU(cr, src2, cr)
				break
			}
			// General case. Need to do two different subtracts (src2 - cr - src1),
			// computing carry bits for both, and add'ing them together.
			// Doing src2 - cr first frees up cr to store the carry from the sub of src1.
			a.SLTU(cr, src2, tmp)
			a.EOL("SBCS %s, %s, %s", src1, src2, dst)
			a.Sub(cr, src2, dst, KeepCarry)
			a.EOL("...")
			a.SLTU(src1, dst, cr)
			a.EOL("...")
			a.Sub(src1, dst, dst, KeepCarry)
			a.EOL("...")
			a.Add(tmp, cr, cr, KeepCarry)
			a.EOL("...")
		}
	}
}

// ClearCarry clears the carry flag.
// The ‘which’ parameter must be AddCarry or SubCarry to specify how the flag will be used.
// (On some systems, the sub carry's actual processor bit is inverted from its usual value.)
func (a *Asm) ClearCarry(which Carry) {
	dst := Reg{a.Arch.regs[0]} // not actually modified
	switch which & (AddCarry | SubCarry) {
	default:
		a.Fatalf("bad carry")
	case AddCarry:
		a.Add(a.Imm(0), dst, dst, SetCarry|which&AltCarry)
	case SubCarry:
		a.Sub(a.Imm(0), dst, dst, SetCarry|which&AltCarry)
	}
	a.EOL("clear carry")
}

// SaveCarry saves the carry flag into dst.
// The meaning of the bits in dst is architecture-dependent.
// The carry flag is left in an undefined state.
func (a *Asm) SaveCarry(dst Reg) {
	// Note: As implemented here, the carry flag is actually left unmodified,
	// but we say it is in an undefined state in case that changes in the future.
	// (The SmashCarry could be changed to SetCarry if so.)
	if cr := a.Carry(); cr.Valid() {
		if cr == dst {
			return // avoid EOL
		}
		a.Mov(cr, dst)
	} else {
		a.Sub(dst, dst, dst, UseCarry|SmashCarry)
	}
	a.EOL("save carry")
}

// RestoreCarry restores the carry flag from src.
// src is left in an undefined state.
func (a *Asm) RestoreCarry(src Reg) {
	if cr := a.Carry(); cr.Valid() {
		if cr == src {
			return // avoid EOL
		}
		a.Mov(src, cr)
	} else if a.Arch.subCarryIsBorrow {
		a.Add(src, src, src, SetCarry)
	} else {
		// SaveCarry saved the sub carry flag with an encoding of 0, 1 -> 0, ^0.
		// Restore it by subtracting from a value less than ^0, which will carry if src != 0.
		// If there is no zero register, the SP register is guaranteed to be less than ^0.
		// (This may seem too clever, but on GOARCH=arm we have no other good options.)
		a.Sub(src, cmp.Or(a.ZR(), Reg{"SP"}), src, SetCarry)
	}
	a.EOL("restore carry")
}

// ConvertCarry converts the carry flag in dst from the internal format to a 0 or 1.
// The carry flag is left in an undefined state.
func (a *Asm) ConvertCarry(which Carry, dst Reg) {
	if a.Carry().Valid() { // already 0 or 1
		return
	}
	switch which {
	case AddCarry:
		if a.Arch.subCarryIsBorrow {
			a.Neg(dst, dst)
		} else {
			a.Add(a.Imm(1), dst, dst, SmashCarry)
		}
		a.EOL("convert add carry")
	case SubCarry:
		a.Neg(dst, dst)
		a.EOL("convert sub carry")
	}
}

// SaveConvertCarry saves and converts the carry flag into dst: 0 unset, 1 set.
// The carry flag is left in an undefined state.
func (a *Asm) SaveConvertCarry(which Carry, dst Reg) {
	switch which {
	default:
		a.Fatalf("bad carry")
	case AddCarry:
		if (a.Arch.adc != "" || a.Arch.adcs != "") && a.ZR().Valid() {
			a.Add(a.ZR(), a.ZR(), dst, UseCarry|SmashCarry)
			a.EOL("save & convert add carry")
			return
		}
	case SubCarry:
		// no special cases
	}
	a.SaveCarry(dst)
	a.ConvertCarry(which, dst)
}

// MulWide emits dstlo = src1 * src2 and dsthi = (src1 * src2) >> WordBits.
// The carry flag is left in an undefined state.
// If dstlo or dsthi is the zero Reg, then those outputs are discarded.
func (a *Asm) MulWide(src1, src2, dstlo, dsthi Reg) {
	switch {
	default:
		a.Fatalf("mulwide not available")
	case a.Arch.mulWideF != nil:
		a.Arch.mulWideF(a, src1, src2, dstlo, dsthi)
	case a.Arch.mul != "" && !dsthi.Valid():
		a.op3(a.Arch.mul, src1, src2, dstlo)
	case a.Arch.mulhi != "" && !dstlo.Valid():
		a.op3(a.Arch.mulhi, src1, src2, dsthi)
	case a.Arch.mul != "" && a.Arch.mulhi != "" && dstlo != src1 && dstlo != src2:
		a.op3(a.Arch.mul, src1, src2, dstlo)
		a.op3(a.Arch.mulhi, src1, src2, dsthi)
	case a.Arch.mul != "" && a.Arch.mulhi != "" && dsthi != src1 && dsthi != src2:
		a.op3(a.Arch.mulhi, src1, src2, dsthi)
		a.op3(a.Arch.mul, src1, src2, dstlo)
	}
}

// Jmp jumps to the label.
func (a *Asm) Jmp(label string) {
	// Note: Some systems prefer the spelling B or BR, but all accept JMP.
	a.Printf("\tJMP %s\n", label)
}

// JmpZero jumps to the label if src is zero.
// It may modify the carry flag unless a.Arch.CarrySafeLoop is true.
func (a *Asm) JmpZero(src Reg, label string) {
	a.Printf("\t"+a.Arch.jmpZero+"\n", src, label)
}

// JmpNonZero jumps to the label if src is non-zero.
// It may modify the carry flag unless a.Arch,CarrySafeLoop is true.
func (a *Asm) JmpNonZero(src Reg, label string) {
	a.Printf("\t"+a.Arch.jmpNonZero+"\n", src, label)
}

// Label emits a label with the given name.
func (a *Asm) Label(name string) {
	a.Printf("%s:\n", name)
}

// Ret returns.
func (a *Asm) Ret() {
	a.Printf("\tRET\n")
}
