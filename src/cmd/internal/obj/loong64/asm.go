// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package loong64

import (
	"cmd/internal/obj"
	"cmd/internal/objabi"
	"fmt"
	"log"
	"math/bits"
	"slices"
)

// ctxt0 holds state while assembling a single function.
// Each function gets a fresh ctxt0.
// This allows for multiple functions to be safely concurrently assembled.
type ctxt0 struct {
	ctxt       *obj.Link
	newprog    obj.ProgAlloc
	cursym     *obj.LSym
	autosize   int32
	instoffset int64
	pc         int64
}

// Instruction layout.

const (
	FuncAlign = 4
	loopAlign = 16
)

type Optab struct {
	as    obj.As
	from1 uint8
	reg   uint8
	from3 uint8
	to1   uint8
	to2   uint8
	type_ int8
	size  int8
	param int16
	flag  uint8
}

const (
	NOTUSETMP = 1 << iota // p expands to multiple instructions, but does NOT use REGTMP

	// branchLoopHead marks loop entry.
	// Used to insert padding for under-aligned loops.
	branchLoopHead
)

var optab = []Optab{
	{obj.ATEXT, C_ADDR, C_NONE, C_NONE, C_TEXTSIZE, C_NONE, 0, 0, 0, 0},

	{AMOVW, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 1, 4, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 1, 4, 0, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 12, 4, 0, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 12, 4, 0, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 12, 4, 0, 0},

	{ASUB, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASUBV, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AADD, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AADDV, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AAND, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASUB, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASUBV, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AADD, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AADDV, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AAND, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ANEGW, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ANEGV, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AMASKEQZ, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASLL, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASLL, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASLLV, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{ASLLV, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AMUL, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AMUL, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AMULV, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AMULV, C_REG, C_REG, C_NONE, C_REG, C_NONE, 2, 4, 0, 0},
	{AADDF, C_FREG, C_NONE, C_NONE, C_FREG, C_NONE, 2, 4, 0, 0},
	{AADDF, C_FREG, C_FREG, C_NONE, C_FREG, C_NONE, 2, 4, 0, 0},
	{ACMPEQF, C_FREG, C_FREG, C_NONE, C_FCCREG, C_NONE, 2, 4, 0, 0},

	{AVSEQB, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVSEQB, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AVSEQB, C_S5CON, C_VREG, C_NONE, C_VREG, C_NONE, 22, 4, 0, 0},
	{AXVSEQB, C_S5CON, C_XREG, C_NONE, C_XREG, C_NONE, 22, 4, 0, 0},
	{AVANDV, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AVANDV, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVANDV, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AXVANDV, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AVANDB, C_U8CON, C_VREG, C_NONE, C_VREG, C_NONE, 23, 4, 0, 0},
	{AVANDB, C_U8CON, C_NONE, C_NONE, C_VREG, C_NONE, 23, 4, 0, 0},
	{AXVANDB, C_U8CON, C_XREG, C_NONE, C_XREG, C_NONE, 23, 4, 0, 0},
	{AXVANDB, C_U8CON, C_NONE, C_NONE, C_XREG, C_NONE, 23, 4, 0, 0},

	{AVADDB, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AVADDB, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVADDB, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AXVADDB, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},

	{AVSLLB, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AVSLLB, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLB, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLB, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AVSLLB, C_U3CON, C_VREG, C_NONE, C_VREG, C_NONE, 13, 4, 0, 0},
	{AXVSLLB, C_U3CON, C_XREG, C_NONE, C_XREG, C_NONE, 13, 4, 0, 0},
	{AVSLLB, C_U3CON, C_NONE, C_NONE, C_VREG, C_NONE, 13, 4, 0, 0},
	{AXVSLLB, C_U3CON, C_NONE, C_NONE, C_XREG, C_NONE, 13, 4, 0, 0},

	{AVSLLH, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AVSLLH, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLH, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLH, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AVSLLH, C_U4CON, C_VREG, C_NONE, C_VREG, C_NONE, 14, 4, 0, 0},
	{AXVSLLH, C_U4CON, C_XREG, C_NONE, C_XREG, C_NONE, 14, 4, 0, 0},
	{AVSLLH, C_U4CON, C_NONE, C_NONE, C_VREG, C_NONE, 14, 4, 0, 0},
	{AXVSLLH, C_U4CON, C_NONE, C_NONE, C_XREG, C_NONE, 14, 4, 0, 0},

	{AVSLLW, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AVSLLW, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLW, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLW, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AVSLLW, C_U5CON, C_VREG, C_NONE, C_VREG, C_NONE, 31, 4, 0, 0},
	{AXVSLLW, C_U5CON, C_XREG, C_NONE, C_XREG, C_NONE, 31, 4, 0, 0},
	{AVSLLW, C_U5CON, C_NONE, C_NONE, C_VREG, C_NONE, 31, 4, 0, 0},
	{AXVSLLW, C_U5CON, C_NONE, C_NONE, C_XREG, C_NONE, 31, 4, 0, 0},

	{AVSLLV, C_VREG, C_VREG, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AVSLLV, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLV, C_XREG, C_XREG, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AXVSLLV, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 2, 4, 0, 0},
	{AVSLLV, C_U6CON, C_VREG, C_NONE, C_VREG, C_NONE, 32, 4, 0, 0},
	{AXVSLLV, C_U6CON, C_XREG, C_NONE, C_XREG, C_NONE, 32, 4, 0, 0},
	{AVSLLV, C_U6CON, C_NONE, C_NONE, C_VREG, C_NONE, 32, 4, 0, 0},
	{AXVSLLV, C_U6CON, C_NONE, C_NONE, C_XREG, C_NONE, 32, 4, 0, 0},

	{ACLOW, C_REG, C_NONE, C_NONE, C_REG, C_NONE, 9, 4, 0, 0},
	{AABSF, C_FREG, C_NONE, C_NONE, C_FREG, C_NONE, 9, 4, 0, 0},
	{AMOVVF, C_FREG, C_NONE, C_NONE, C_FREG, C_NONE, 9, 4, 0, 0},
	{AMOVF, C_FREG, C_NONE, C_NONE, C_FREG, C_NONE, 9, 4, 0, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_FREG, C_NONE, 9, 4, 0, 0},
	{AVPCNTB, C_VREG, C_NONE, C_NONE, C_VREG, C_NONE, 9, 4, 0, 0},
	{AXVPCNTB, C_XREG, C_NONE, C_NONE, C_XREG, C_NONE, 9, 4, 0, 0},
	{AVSETEQV, C_VREG, C_NONE, C_NONE, C_FCCREG, C_NONE, 9, 4, 0, 0},
	{AXVSETEQV, C_XREG, C_NONE, C_NONE, C_FCCREG, C_NONE, 9, 4, 0, 0},

	{AFMADDF, C_FREG, C_FREG, C_NONE, C_FREG, C_NONE, 37, 4, 0, 0},
	{AFMADDF, C_FREG, C_FREG, C_FREG, C_FREG, C_NONE, 37, 4, 0, 0},

	{AMOVW, C_REG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGSP, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGSP, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGSP, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGSP, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGSP, 0},
	{AMOVW, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AVMOVQ, C_VREG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AXVMOVQ, C_XREG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{AVMOVQ, C_VREG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGZERO, 0},
	{AXVMOVQ, C_XREG, C_NONE, C_NONE, C_SAUTO, C_NONE, 7, 4, REGZERO, 0},
	{ASC, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},
	{ASCV, C_REG, C_NONE, C_NONE, C_SOREG, C_NONE, 7, 4, REGZERO, 0},

	{AMOVW, C_SAUTO, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGSP, 0},
	{AMOVWU, C_SAUTO, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGSP, 0},
	{AMOVV, C_SAUTO, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGSP, 0},
	{AMOVB, C_SAUTO, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGSP, 0},
	{AMOVBU, C_SAUTO, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGSP, 0},
	{AMOVW, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},
	{AMOVWU, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},
	{AMOVV, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},
	{AMOVB, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},
	{AMOVBU, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},
	{AVMOVQ, C_SOREG, C_NONE, C_NONE, C_VREG, C_NONE, 8, 4, REGZERO, 0},
	{AXVMOVQ, C_SOREG, C_NONE, C_NONE, C_XREG, C_NONE, 8, 4, REGZERO, 0},
	{AVMOVQ, C_SAUTO, C_NONE, C_NONE, C_VREG, C_NONE, 8, 4, REGZERO, 0},
	{AXVMOVQ, C_SAUTO, C_NONE, C_NONE, C_XREG, C_NONE, 8, 4, REGZERO, 0},
	{ALL, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},
	{ALLV, C_SOREG, C_NONE, C_NONE, C_REG, C_NONE, 8, 4, REGZERO, 0},

	{AMOVW, C_REG, C_NONE, C_NONE, C_LAUTO, C_NONE, 35, 12, REGSP, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_LAUTO, C_NONE, 35, 12, REGSP, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_LAUTO, C_NONE, 35, 12, REGSP, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_LAUTO, C_NONE, 35, 12, REGSP, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_LAUTO, C_NONE, 35, 12, REGSP, 0},
	{AMOVW, C_REG, C_NONE, C_NONE, C_LOREG, C_NONE, 35, 12, REGZERO, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_LOREG, C_NONE, 35, 12, REGZERO, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_LOREG, C_NONE, 35, 12, REGZERO, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_LOREG, C_NONE, 35, 12, REGZERO, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_LOREG, C_NONE, 35, 12, REGZERO, 0},
	{ASC, C_REG, C_NONE, C_NONE, C_LOREG, C_NONE, 35, 12, REGZERO, 0},
	{AMOVW, C_REG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},
	{AMOVW, C_REG, C_NONE, C_NONE, C_TLS_LE, C_NONE, 53, 16, 0, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_TLS_LE, C_NONE, 53, 16, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_TLS_LE, C_NONE, 53, 16, 0, 0},
	{AMOVB, C_REG, C_NONE, C_NONE, C_TLS_LE, C_NONE, 53, 16, 0, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_TLS_LE, C_NONE, 53, 16, 0, 0},

	{AMOVW, C_LAUTO, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGSP, 0},
	{AMOVWU, C_LAUTO, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGSP, 0},
	{AMOVV, C_LAUTO, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGSP, 0},
	{AMOVB, C_LAUTO, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGSP, 0},
	{AMOVBU, C_LAUTO, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGSP, 0},
	{AMOVW, C_LOREG, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGZERO, 0},
	{AMOVWU, C_LOREG, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGZERO, 0},
	{AMOVV, C_LOREG, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGZERO, 0},
	{AMOVB, C_LOREG, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGZERO, 0},
	{AMOVBU, C_LOREG, C_NONE, C_NONE, C_REG, C_NONE, 36, 12, REGZERO, 0},
	{AMOVW, C_ADDR, C_NONE, C_NONE, C_REG, C_NONE, 51, 8, 0, 0},
	{AMOVWU, C_ADDR, C_NONE, C_NONE, C_REG, C_NONE, 51, 8, 0, 0},
	{AMOVV, C_ADDR, C_NONE, C_NONE, C_REG, C_NONE, 51, 8, 0, 0},
	{AMOVB, C_ADDR, C_NONE, C_NONE, C_REG, C_NONE, 51, 8, 0, 0},
	{AMOVBU, C_ADDR, C_NONE, C_NONE, C_REG, C_NONE, 51, 8, 0, 0},
	{AMOVW, C_TLS_LE, C_NONE, C_NONE, C_REG, C_NONE, 54, 16, 0, 0},
	{AMOVWU, C_TLS_LE, C_NONE, C_NONE, C_REG, C_NONE, 54, 16, 0, 0},
	{AMOVV, C_TLS_LE, C_NONE, C_NONE, C_REG, C_NONE, 54, 16, 0, 0},
	{AMOVB, C_TLS_LE, C_NONE, C_NONE, C_REG, C_NONE, 54, 16, 0, 0},
	{AMOVBU, C_TLS_LE, C_NONE, C_NONE, C_REG, C_NONE, 54, 16, 0, 0},

	{AMOVW, C_SACON, C_NONE, C_NONE, C_REG, C_NONE, 3, 4, REGSP, 0},
	{AMOVV, C_SACON, C_NONE, C_NONE, C_REG, C_NONE, 3, 4, REGSP, 0},
	{AMOVW, C_EXTADDR, C_NONE, C_NONE, C_REG, C_NONE, 52, 8, 0, NOTUSETMP},
	{AMOVV, C_EXTADDR, C_NONE, C_NONE, C_REG, C_NONE, 52, 8, 0, NOTUSETMP},

	{AMOVW, C_LACON, C_NONE, C_NONE, C_REG, C_NONE, 27, 12, REGSP, 0},
	{AMOVV, C_LACON, C_NONE, C_NONE, C_REG, C_NONE, 27, 12, REGSP, 0},
	{AMOVW, C_12CON, C_NONE, C_NONE, C_REG, C_NONE, 3, 4, REGZERO, 0},
	{AMOVV, C_12CON, C_NONE, C_NONE, C_REG, C_NONE, 3, 4, REGZERO, 0},

	{AMOVW, C_32CON20_0, C_NONE, C_NONE, C_REG, C_NONE, 25, 4, 0, 0},
	{AMOVV, C_32CON20_0, C_NONE, C_NONE, C_REG, C_NONE, 25, 4, 0, 0},
	{AMOVW, C_32CON, C_NONE, C_NONE, C_REG, C_NONE, 19, 8, 0, NOTUSETMP},
	{AMOVV, C_32CON, C_NONE, C_NONE, C_REG, C_NONE, 19, 8, 0, NOTUSETMP},
	{AMOVV, C_DCON12_0, C_NONE, C_NONE, C_REG, C_NONE, 67, 4, 0, NOTUSETMP},
	{AMOVV, C_DCON12_20S, C_NONE, C_NONE, C_REG, C_NONE, 68, 8, 0, NOTUSETMP},
	{AMOVV, C_DCON32_12S, C_NONE, C_NONE, C_REG, C_NONE, 69, 12, 0, NOTUSETMP},
	{AMOVV, C_DCON, C_NONE, C_NONE, C_REG, C_NONE, 59, 16, 0, NOTUSETMP},

	{AADD, C_US12CON, C_REG, C_NONE, C_REG, C_NONE, 4, 4, 0, 0},
	{AADD, C_US12CON, C_NONE, C_NONE, C_REG, C_NONE, 4, 4, 0, 0},
	{AADD, C_U12CON, C_REG, C_NONE, C_REG, C_NONE, 10, 8, 0, 0},
	{AADD, C_U12CON, C_NONE, C_NONE, C_REG, C_NONE, 10, 8, 0, 0},

	{AADDV, C_US12CON, C_REG, C_NONE, C_REG, C_NONE, 4, 4, 0, 0},
	{AADDV, C_US12CON, C_NONE, C_NONE, C_REG, C_NONE, 4, 4, 0, 0},
	{AADDV, C_U12CON, C_REG, C_NONE, C_REG, C_NONE, 10, 8, 0, 0},
	{AADDV, C_U12CON, C_NONE, C_NONE, C_REG, C_NONE, 10, 8, 0, 0},

	{AAND, C_UU12CON, C_REG, C_NONE, C_REG, C_NONE, 4, 4, 0, 0},
	{AAND, C_UU12CON, C_NONE, C_NONE, C_REG, C_NONE, 4, 4, 0, 0},
	{AAND, C_S12CON, C_REG, C_NONE, C_REG, C_NONE, 10, 8, 0, 0},
	{AAND, C_S12CON, C_NONE, C_NONE, C_REG, C_NONE, 10, 8, 0, 0},

	{AADD, C_32CON20_0, C_REG, C_NONE, C_REG, C_NONE, 26, 8, 0, 0},
	{AADD, C_32CON20_0, C_NONE, C_NONE, C_REG, C_NONE, 26, 8, 0, 0},
	{AADDV, C_32CON20_0, C_REG, C_NONE, C_REG, C_NONE, 26, 8, 0, 0},
	{AADDV, C_32CON20_0, C_NONE, C_NONE, C_REG, C_NONE, 26, 8, 0, 0},
	{AAND, C_32CON20_0, C_REG, C_NONE, C_REG, C_NONE, 26, 8, 0, 0},
	{AAND, C_32CON20_0, C_NONE, C_NONE, C_REG, C_NONE, 26, 8, 0, 0},

	{AADD, C_32CON, C_NONE, C_NONE, C_REG, C_NONE, 24, 12, 0, 0},
	{AADDV, C_32CON, C_NONE, C_NONE, C_REG, C_NONE, 24, 12, 0, 0},
	{AAND, C_32CON, C_NONE, C_NONE, C_REG, C_NONE, 24, 12, 0, 0},
	{AADD, C_32CON, C_REG, C_NONE, C_REG, C_NONE, 24, 12, 0, 0},
	{AADDV, C_32CON, C_REG, C_NONE, C_REG, C_NONE, 24, 12, 0, 0},
	{AAND, C_32CON, C_REG, C_NONE, C_REG, C_NONE, 24, 12, 0, 0},

	{AADDV, C_DCON, C_NONE, C_NONE, C_REG, C_NONE, 60, 20, 0, 0},
	{AADDV, C_DCON, C_REG, C_NONE, C_REG, C_NONE, 60, 20, 0, 0},
	{AAND, C_DCON, C_NONE, C_NONE, C_REG, C_NONE, 60, 20, 0, 0},
	{AAND, C_DCON, C_REG, C_NONE, C_REG, C_NONE, 60, 20, 0, 0},
	{AADDV, C_DCON12_0, C_NONE, C_NONE, C_REG, C_NONE, 70, 8, 0, 0},
	{AADDV, C_DCON12_0, C_REG, C_NONE, C_REG, C_NONE, 70, 8, 0, 0},
	{AAND, C_DCON12_0, C_NONE, C_NONE, C_REG, C_NONE, 70, 8, 0, 0},
	{AAND, C_DCON12_0, C_REG, C_NONE, C_REG, C_NONE, 70, 8, 0, 0},
	{AADDV, C_DCON12_20S, C_NONE, C_NONE, C_REG, C_NONE, 71, 12, 0, 0},
	{AADDV, C_DCON12_20S, C_REG, C_NONE, C_REG, C_NONE, 71, 12, 0, 0},
	{AAND, C_DCON12_20S, C_NONE, C_NONE, C_REG, C_NONE, 71, 12, 0, 0},
	{AAND, C_DCON12_20S, C_REG, C_NONE, C_REG, C_NONE, 71, 12, 0, 0},
	{AADDV, C_DCON32_12S, C_NONE, C_NONE, C_REG, C_NONE, 72, 16, 0, 0},
	{AADDV, C_DCON32_12S, C_REG, C_NONE, C_REG, C_NONE, 72, 16, 0, 0},
	{AAND, C_DCON32_12S, C_NONE, C_NONE, C_REG, C_NONE, 72, 16, 0, 0},
	{AAND, C_DCON32_12S, C_REG, C_NONE, C_REG, C_NONE, 72, 16, 0, 0},

	{ASLL, C_U5CON, C_REG, C_NONE, C_REG, C_NONE, 16, 4, 0, 0},
	{ASLL, C_U5CON, C_NONE, C_NONE, C_REG, C_NONE, 16, 4, 0, 0},

	{ASLLV, C_U6CON, C_REG, C_NONE, C_REG, C_NONE, 16, 4, 0, 0},
	{ASLLV, C_U6CON, C_NONE, C_NONE, C_REG, C_NONE, 16, 4, 0, 0},

	{ABSTRPICKW, C_U6CON, C_REG, C_U6CON, C_REG, C_NONE, 17, 4, 0, 0},
	{ABSTRPICKW, C_U6CON, C_REG, C_ZCON, C_REG, C_NONE, 17, 4, 0, 0},
	{ABSTRPICKW, C_ZCON, C_REG, C_ZCON, C_REG, C_NONE, 17, 4, 0, 0},

	{ASYSCALL, C_NONE, C_NONE, C_NONE, C_NONE, C_NONE, 5, 4, 0, 0},
	{ASYSCALL, C_U15CON, C_NONE, C_NONE, C_NONE, C_NONE, 5, 4, 0, 0},

	{ABEQ, C_REG, C_REG, C_NONE, C_BRAN, C_NONE, 6, 4, 0, 0},
	{ABEQ, C_REG, C_NONE, C_NONE, C_BRAN, C_NONE, 6, 4, 0, 0},
	{ABLEZ, C_REG, C_NONE, C_NONE, C_BRAN, C_NONE, 6, 4, 0, 0},
	{ABFPT, C_NONE, C_NONE, C_NONE, C_BRAN, C_NONE, 6, 4, 0, 0},
	{ABFPT, C_FCCREG, C_NONE, C_NONE, C_BRAN, C_NONE, 6, 4, 0, 0},

	{AJMP, C_NONE, C_NONE, C_NONE, C_BRAN, C_NONE, 11, 4, 0, 0}, // b
	{AJAL, C_NONE, C_NONE, C_NONE, C_BRAN, C_NONE, 11, 4, 0, 0}, // bl

	{AJMP, C_NONE, C_NONE, C_NONE, C_ZOREG, C_NONE, 18, 4, REGZERO, 0}, // jirl r0, rj, 0
	{AJAL, C_NONE, C_NONE, C_NONE, C_ZOREG, C_NONE, 18, 4, REGLINK, 0}, // jirl r1, rj, 0

	{AMOVF, C_SAUTO, C_NONE, C_NONE, C_FREG, C_NONE, 28, 4, REGSP, 0},
	{AMOVD, C_SAUTO, C_NONE, C_NONE, C_FREG, C_NONE, 28, 4, REGSP, 0},
	{AMOVF, C_SOREG, C_NONE, C_NONE, C_FREG, C_NONE, 28, 4, REGZERO, 0},
	{AMOVD, C_SOREG, C_NONE, C_NONE, C_FREG, C_NONE, 28, 4, REGZERO, 0},

	{AMOVF, C_LAUTO, C_NONE, C_NONE, C_FREG, C_NONE, 28, 12, REGSP, 0},
	{AMOVD, C_LAUTO, C_NONE, C_NONE, C_FREG, C_NONE, 28, 12, REGSP, 0},
	{AMOVF, C_LOREG, C_NONE, C_NONE, C_FREG, C_NONE, 28, 12, REGZERO, 0},
	{AMOVD, C_LOREG, C_NONE, C_NONE, C_FREG, C_NONE, 28, 12, REGZERO, 0},
	{AMOVF, C_ADDR, C_NONE, C_NONE, C_FREG, C_NONE, 51, 8, 0, 0},
	{AMOVD, C_ADDR, C_NONE, C_NONE, C_FREG, C_NONE, 51, 8, 0, 0},

	{AMOVF, C_FREG, C_NONE, C_NONE, C_SAUTO, C_NONE, 29, 4, REGSP, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_SAUTO, C_NONE, 29, 4, REGSP, 0},
	{AMOVF, C_FREG, C_NONE, C_NONE, C_SOREG, C_NONE, 29, 4, REGZERO, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_SOREG, C_NONE, 29, 4, REGZERO, 0},

	{AMOVF, C_FREG, C_NONE, C_NONE, C_LAUTO, C_NONE, 29, 12, REGSP, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_LAUTO, C_NONE, 29, 12, REGSP, 0},
	{AMOVF, C_FREG, C_NONE, C_NONE, C_LOREG, C_NONE, 29, 12, REGZERO, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_LOREG, C_NONE, 29, 12, REGZERO, 0},
	{AMOVF, C_FREG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_ADDR, C_NONE, 50, 8, 0, 0},

	{AMOVW, C_REG, C_NONE, C_NONE, C_FREG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_FREG, C_NONE, 30, 4, 0, 0},
	{AMOVW, C_FREG, C_NONE, C_NONE, C_REG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_FREG, C_NONE, C_NONE, C_REG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_FCCREG, C_NONE, C_NONE, C_REG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_FCSRREG, C_NONE, C_NONE, C_REG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_FCCREG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_FCSRREG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_FREG, C_NONE, C_NONE, C_FCCREG, C_NONE, 30, 4, 0, 0},
	{AMOVV, C_FCCREG, C_NONE, C_NONE, C_FREG, C_NONE, 30, 4, 0, 0},

	{AMOVW, C_12CON, C_NONE, C_NONE, C_FREG, C_NONE, 34, 8, 0, 0},

	{AMOVB, C_REG, C_NONE, C_NONE, C_TLS_IE, C_NONE, 56, 16, 0, 0},
	{AMOVW, C_REG, C_NONE, C_NONE, C_TLS_IE, C_NONE, 56, 16, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_TLS_IE, C_NONE, 56, 16, 0, 0},
	{AMOVBU, C_REG, C_NONE, C_NONE, C_TLS_IE, C_NONE, 56, 16, 0, 0},
	{AMOVWU, C_REG, C_NONE, C_NONE, C_TLS_IE, C_NONE, 56, 16, 0, 0},

	{AMOVB, C_TLS_IE, C_NONE, C_NONE, C_REG, C_NONE, 57, 16, 0, 0},
	{AMOVW, C_TLS_IE, C_NONE, C_NONE, C_REG, C_NONE, 57, 16, 0, 0},
	{AMOVV, C_TLS_IE, C_NONE, C_NONE, C_REG, C_NONE, 57, 16, 0, 0},
	{AMOVBU, C_TLS_IE, C_NONE, C_NONE, C_REG, C_NONE, 57, 16, 0, 0},
	{AMOVWU, C_TLS_IE, C_NONE, C_NONE, C_REG, C_NONE, 57, 16, 0, 0},

	{AWORD, C_32CON, C_NONE, C_NONE, C_NONE, C_NONE, 38, 4, 0, 0},
	{AWORD, C_DCON, C_NONE, C_NONE, C_NONE, C_NONE, 61, 4, 0, 0},

	{AMOVV, C_GOTADDR, C_NONE, C_NONE, C_REG, C_NONE, 65, 8, 0, 0},

	{ATEQ, C_US12CON, C_REG, C_NONE, C_REG, C_NONE, 15, 8, 0, 0},
	{ATEQ, C_US12CON, C_NONE, C_NONE, C_REG, C_NONE, 15, 8, 0, 0},

	{ARDTIMELW, C_NONE, C_NONE, C_NONE, C_REG, C_REG, 62, 4, 0, 0},
	{AAMSWAPW, C_REG, C_NONE, C_NONE, C_ZOREG, C_REG, 66, 4, 0, 0},
	{ANOOP, C_NONE, C_NONE, C_NONE, C_NONE, C_NONE, 49, 4, 0, 0},

	/* store with extended register offset */
	{AMOVB, C_REG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},
	{AMOVW, C_REG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},
	{AMOVV, C_REG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},
	{AMOVF, C_FREG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},
	{AMOVD, C_FREG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},
	{AVMOVQ, C_VREG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},
	{AXVMOVQ, C_XREG, C_NONE, C_NONE, C_ROFF, C_NONE, 20, 4, 0, 0},

	/* load with extended register offset */
	{AMOVB, C_ROFF, C_NONE, C_NONE, C_REG, C_NONE, 21, 4, 0, 0},
	{AMOVBU, C_ROFF, C_NONE, C_NONE, C_REG, C_NONE, 21, 4, 0, 0},
	{AMOVW, C_ROFF, C_NONE, C_NONE, C_REG, C_NONE, 21, 4, 0, 0},
	{AMOVWU, C_ROFF, C_NONE, C_NONE, C_REG, C_NONE, 21, 4, 0, 0},
	{AMOVV, C_ROFF, C_NONE, C_NONE, C_REG, C_NONE, 21, 4, 0, 0},
	{AMOVF, C_ROFF, C_NONE, C_NONE, C_FREG, C_NONE, 21, 4, 0, 0},
	{AMOVD, C_ROFF, C_NONE, C_NONE, C_FREG, C_NONE, 21, 4, 0, 0},
	{AVMOVQ, C_ROFF, C_NONE, C_NONE, C_VREG, C_NONE, 21, 4, 0, 0},
	{AXVMOVQ, C_ROFF, C_NONE, C_NONE, C_XREG, C_NONE, 21, 4, 0, 0},

	{AVMOVQ, C_REG, C_NONE, C_NONE, C_ELEM, C_NONE, 39, 4, 0, 0},
	{AVMOVQ, C_ELEM, C_NONE, C_NONE, C_REG, C_NONE, 40, 4, 0, 0},
	{AXVMOVQ, C_REG, C_NONE, C_NONE, C_ELEM, C_NONE, 39, 4, 0, 0},
	{AXVMOVQ, C_ELEM, C_NONE, C_NONE, C_REG, C_NONE, 40, 4, 0, 0},

	{AXVMOVQ, C_XREG, C_NONE, C_NONE, C_ELEM, C_NONE, 43, 4, 0, 0},
	{AXVMOVQ, C_ELEM, C_NONE, C_NONE, C_XREG, C_NONE, 44, 4, 0, 0},

	{AVMOVQ, C_REG, C_NONE, C_NONE, C_ARNG, C_NONE, 41, 4, 0, 0},
	{AXVMOVQ, C_REG, C_NONE, C_NONE, C_ARNG, C_NONE, 41, 4, 0, 0},
	{AXVMOVQ, C_XREG, C_NONE, C_NONE, C_ARNG, C_NONE, 42, 4, 0, 0},

	{AVMOVQ, C_ELEM, C_NONE, C_NONE, C_ARNG, C_NONE, 45, 4, 0, 0},

	{APRELD, C_SOREG, C_NONE, C_U5CON, C_NONE, C_NONE, 46, 4, 0, 0},
	{APRELDX, C_ROFF, C_NONE, C_U5CON, C_NONE, C_NONE, 47, 4, 0, 0},

	{obj.APCALIGN, C_U12CON, C_NONE, C_NONE, C_NONE, C_NONE, 0, 0, 0, 0},
	{obj.APCDATA, C_32CON, C_NONE, C_NONE, C_32CON, C_NONE, 0, 0, 0, 0},
	{obj.APCDATA, C_DCON, C_NONE, C_NONE, C_DCON, C_NONE, 0, 0, 0, 0},
	{obj.AFUNCDATA, C_U12CON, C_NONE, C_NONE, C_ADDR, C_NONE, 0, 0, 0, 0},
	{obj.ANOP, C_NONE, C_NONE, C_NONE, C_NONE, C_NONE, 0, 0, 0, 0},
	{obj.ANOP, C_32CON, C_NONE, C_NONE, C_NONE, C_NONE, 0, 0, 0, 0}, // nop variants, see #40689
	{obj.ANOP, C_DCON, C_NONE, C_NONE, C_NONE, C_NONE, 0, 0, 0, 0},  // nop variants, see #40689
	{obj.ANOP, C_REG, C_NONE, C_NONE, C_NONE, C_NONE, 0, 0, 0, 0},
	{obj.ANOP, C_FREG, C_NONE, C_NONE, C_NONE, C_NONE, 0, 0, 0, 0},
	{obj.ADUFFZERO, C_NONE, C_NONE, C_NONE, C_BRAN, C_NONE, 11, 4, 0, 0}, // same as AJMP
	{obj.ADUFFCOPY, C_NONE, C_NONE, C_NONE, C_BRAN, C_NONE, 11, 4, 0, 0}, // same as AJMP

	{obj.AXXX, C_NONE, C_NONE, C_NONE, C_NONE, C_NONE, 0, 4, 0, 0},
}

var atomicInst = map[obj.As]uint32{
	AAMSWAPB:   0x070B8 << 15, // amswap.b
	AAMSWAPH:   0x070B9 << 15, // amswap.h
	AAMSWAPW:   0x070C0 << 15, // amswap.w
	AAMSWAPV:   0x070C1 << 15, // amswap.d
	AAMCASB:    0x070B0 << 15, // amcas.b
	AAMCASH:    0x070B1 << 15, // amcas.h
	AAMCASW:    0x070B2 << 15, // amcas.w
	AAMCASV:    0x070B3 << 15, // amcas.d
	AAMADDW:    0x070C2 << 15, // amadd.w
	AAMADDV:    0x070C3 << 15, // amadd.d
	AAMANDW:    0x070C4 << 15, // amand.w
	AAMANDV:    0x070C5 << 15, // amand.d
	AAMORW:     0x070C6 << 15, // amor.w
	AAMORV:     0x070C7 << 15, // amor.d
	AAMXORW:    0x070C8 << 15, // amxor.w
	AAMXORV:    0x070C9 << 15, // amxor.d
	AAMMAXW:    0x070CA << 15, // ammax.w
	AAMMAXV:    0x070CB << 15, // ammax.d
	AAMMINW:    0x070CC << 15, // ammin.w
	AAMMINV:    0x070CD << 15, // ammin.d
	AAMMAXWU:   0x070CE << 15, // ammax.wu
	AAMMAXVU:   0x070CF << 15, // ammax.du
	AAMMINWU:   0x070D0 << 15, // ammin.wu
	AAMMINVU:   0x070D1 << 15, // ammin.du
	AAMSWAPDBB: 0x070BC << 15, // amswap_db.b
	AAMSWAPDBH: 0x070BD << 15, // amswap_db.h
	AAMSWAPDBW: 0x070D2 << 15, // amswap_db.w
	AAMSWAPDBV: 0x070D3 << 15, // amswap_db.d
	AAMCASDBB:  0x070B4 << 15, // amcas_db.b
	AAMCASDBH:  0x070B5 << 15, // amcas_db.h
	AAMCASDBW:  0x070B6 << 15, // amcas_db.w
	AAMCASDBV:  0x070B7 << 15, // amcas_db.d
	AAMADDDBW:  0x070D4 << 15, // amadd_db.w
	AAMADDDBV:  0x070D5 << 15, // amadd_db.d
	AAMANDDBW:  0x070D6 << 15, // amand_db.w
	AAMANDDBV:  0x070D7 << 15, // amand_db.d
	AAMORDBW:   0x070D8 << 15, // amor_db.w
	AAMORDBV:   0x070D9 << 15, // amor_db.d
	AAMXORDBW:  0x070DA << 15, // amxor_db.w
	AAMXORDBV:  0x070DB << 15, // amxor_db.d
	AAMMAXDBW:  0x070DC << 15, // ammax_db.w
	AAMMAXDBV:  0x070DD << 15, // ammax_db.d
	AAMMINDBW:  0x070DE << 15, // ammin_db.w
	AAMMINDBV:  0x070DF << 15, // ammin_db.d
	AAMMAXDBWU: 0x070E0 << 15, // ammax_db.wu
	AAMMAXDBVU: 0x070E1 << 15, // ammax_db.du
	AAMMINDBWU: 0x070E2 << 15, // ammin_db.wu
	AAMMINDBVU: 0x070E3 << 15, // ammin_db.du
}

func IsAtomicInst(as obj.As) bool {
	_, ok := atomicInst[as]

	return ok
}

// pcAlignPadLength returns the number of bytes required to align pc to alignedValue,
// reporting an error if alignedValue is not a power of two or is out of range.
func pcAlignPadLength(ctxt *obj.Link, pc int64, alignedValue int64) int {
	if !((alignedValue&(alignedValue-1) == 0) && 8 <= alignedValue && alignedValue <= 2048) {
		ctxt.Diag("alignment value of an instruction must be a power of two and in the range [8, 2048], got %d\n", alignedValue)
	}
	return int(-pc & (alignedValue - 1))
}

var oprange [ALAST & obj.AMask][]Optab

var xcmp [C_NCLASS][C_NCLASS]bool

func span0(ctxt *obj.Link, cursym *obj.LSym, newprog obj.ProgAlloc) {
	if ctxt.Retpoline {
		ctxt.Diag("-spectre=ret not supported on loong64")
		ctxt.Retpoline = false // don't keep printing
	}

	p := cursym.Func().Text
	if p == nil || p.Link == nil { // handle external functions and ELF section symbols
		return
	}

	c := ctxt0{ctxt: ctxt, newprog: newprog, cursym: cursym, autosize: int32(p.To.Offset + ctxt.Arch.FixedFrameSize)}

	if oprange[AOR&obj.AMask] == nil {
		c.ctxt.Diag("loong64 ops not initialized, call loong64.buildop first")
	}

	pc := int64(0)
	p.Pc = pc

	var m int
	var o *Optab
	for p = p.Link; p != nil; p = p.Link {
		p.Pc = pc
		o = c.oplook(p)
		m = int(o.size)
		if m == 0 {
			switch p.As {
			case obj.APCALIGN:
				alignedValue := p.From.Offset
				m = pcAlignPadLength(ctxt, pc, alignedValue)
				// Update the current text symbol alignment value.
				if int32(alignedValue) > cursym.Func().Align {
					cursym.Func().Align = int32(alignedValue)
				}
				break
			case obj.ANOP, obj.AFUNCDATA, obj.APCDATA:
				continue
			default:
				c.ctxt.Diag("zero-width instruction\n%v", p)
			}
		}

		pc += int64(m)
	}

	c.cursym.Size = pc

	// mark loop entry instructions for padding
	// loop entrances are defined as targets of backward branches
	for p = c.cursym.Func().Text.Link; p != nil; p = p.Link {
		if q := p.To.Target(); q != nil && q.Pc < p.Pc {
			q.Mark |= branchLoopHead
		}
	}

	// Run these passes until convergence.
	for {
		rescan := false
		pc = 0
		prev := c.cursym.Func().Text
		for p = prev.Link; p != nil; prev, p = p, p.Link {
			p.Pc = pc
			o = c.oplook(p)

			// Prepend a PCALIGN $loopAlign to each of the loop heads
			// that need padding, if not already done so (because this
			// pass may execute more than once).
			//
			// This needs to come before any pass that look at pc,
			// because pc will be adjusted if padding happens.
			if p.Mark&branchLoopHead != 0 && pc&(loopAlign-1) != 0 &&
				!(prev.As == obj.APCALIGN && prev.From.Offset >= loopAlign) {
				q := c.newprog()
				prev.Link = q
				q.Link = p
				q.Pc = pc
				q.As = obj.APCALIGN
				q.From.Type = obj.TYPE_CONST
				q.From.Offset = loopAlign
				// Don't associate the synthesized PCALIGN with
				// the original source position, for deterministic
				// mapping between source and corresponding asm.
				// q.Pos = p.Pos

				// Manually make the PCALIGN come into effect,
				// since this loop iteration is for p.
				pc += int64(pcAlignPadLength(ctxt, pc, loopAlign))
				p.Pc = pc
				rescan = true
			}

			// very large conditional branches
			//
			// if any procedure is large enough to generate a large SBRA branch, then
			// generate extra passes putting branches around jmps to fix. this is rare.
			if o.type_ == 6 && p.To.Target() != nil {
				otxt := p.To.Target().Pc - pc

				// On loong64, the immediate value field of the conditional branch instructions
				// BFPT and BFPT is 21 bits, and the others are 16 bits. The jump target address
				// is to logically shift the immediate value in the instruction code to the left
				// by 2 bits and then sign extend.
				bound := int64(1 << (18 - 1))

				switch p.As {
				case ABFPT, ABFPF:
					bound = int64(1 << (23 - 1))
				}

				if otxt < -bound || otxt >= bound {
					q := c.newprog()
					q.Link = p.Link
					p.Link = q
					q.As = AJMP
					q.Pos = p.Pos
					q.To.Type = obj.TYPE_BRANCH
					q.To.SetTarget(p.To.Target())
					p.To.SetTarget(q)
					q = c.newprog()
					q.Link = p.Link
					p.Link = q
					q.As = AJMP
					q.Pos = p.Pos
					q.To.Type = obj.TYPE_BRANCH
					q.To.SetTarget(q.Link.Link)
					rescan = true
				}
			}

			m = int(o.size)
			if m == 0 {
				switch p.As {
				case obj.APCALIGN:
					alignedValue := p.From.Offset
					m = pcAlignPadLength(ctxt, pc, alignedValue)
					break
				case obj.ANOP, obj.AFUNCDATA, obj.APCDATA:
					continue
				default:
					c.ctxt.Diag("zero-width instruction\n%v", p)
				}
			}

			pc += int64(m)
		}

		c.cursym.Size = pc

		if !rescan {
			break
		}
	}

	pc += -pc & (FuncAlign - 1)
	c.cursym.Size = pc

	// lay out the code, emitting code and data relocations.

	c.cursym.Grow(c.cursym.Size)

	bp := c.cursym.P
	var i int32
	var out [5]uint32
	for p := c.cursym.Func().Text.Link; p != nil; p = p.Link {
		c.pc = p.Pc
		o = c.oplook(p)
		if int(o.size) > 4*len(out) {
			log.Fatalf("out array in span0 is too small, need at least %d for %v", o.size/4, p)
		}
		if p.As == obj.APCALIGN {
			alignedValue := p.From.Offset
			v := pcAlignPadLength(c.ctxt, p.Pc, alignedValue)
			for i = 0; i < int32(v/4); i++ {
				// emit ANOOP instruction by the padding size
				c.ctxt.Arch.ByteOrder.PutUint32(bp, OP_12IRR(c.opirr(AAND), 0, 0, 0))
				bp = bp[4:]
			}
			continue
		}
		c.asmout(p, o, out[:])
		for i = 0; i < int32(o.size/4); i++ {
			c.ctxt.Arch.ByteOrder.PutUint32(bp, out[i])
			bp = bp[4:]
		}
	}

	// Mark nonpreemptible instruction sequences.
	// We use REGTMP as a scratch register during call injection,
	// so instruction sequences that use REGTMP are unsafe to
	// preempt asynchronously.
	obj.MarkUnsafePoints(c.ctxt, c.cursym.Func().Text, c.newprog, c.isUnsafePoint, c.isRestartable)
}

// isUnsafePoint returns whether p is an unsafe point.
func (c *ctxt0) isUnsafePoint(p *obj.Prog) bool {
	// If p explicitly uses REGTMP, it's unsafe to preempt, because the
	// preemption sequence clobbers REGTMP.
	return p.From.Reg == REGTMP || p.To.Reg == REGTMP || p.Reg == REGTMP
}

// isRestartable returns whether p is a multi-instruction sequence that,
// if preempted, can be restarted.
func (c *ctxt0) isRestartable(p *obj.Prog) bool {
	if c.isUnsafePoint(p) {
		return false
	}
	// If p is a multi-instruction sequence with uses REGTMP inserted by
	// the assembler in order to materialize a large constant/offset, we
	// can restart p (at the start of the instruction sequence), recompute
	// the content of REGTMP, upon async preemption. Currently, all cases
	// of assembler-inserted REGTMP fall into this category.
	// If p doesn't use REGTMP, it can be simply preempted, so we don't
	// mark it.
	o := c.oplook(p)
	return o.size > 4 && o.flag&NOTUSETMP == 0
}

func isint32(v int64) bool {
	return int64(int32(v)) == v
}

func isuint32(v uint64) bool {
	return uint64(uint32(v)) == v
}

func (c *ctxt0) aclass(a *obj.Addr) int {
	switch a.Type {
	case obj.TYPE_NONE:
		return C_NONE

	case obj.TYPE_REG:
		return c.rclass(a.Reg)

	case obj.TYPE_MEM:
		switch a.Name {
		case obj.NAME_EXTERN,
			obj.NAME_STATIC:
			if a.Sym == nil {
				break
			}
			c.instoffset = a.Offset
			if a.Sym.Type == objabi.STLSBSS {
				if c.ctxt.Flag_shared {
					return C_TLS_IE
				} else {
					return C_TLS_LE
				}
			}
			return C_ADDR

		case obj.NAME_AUTO:
			if a.Reg == REGSP {
				// unset base register for better printing, since
				// a.Offset is still relative to pseudo-SP.
				a.Reg = obj.REG_NONE
			}
			c.instoffset = int64(c.autosize) + a.Offset
			if c.instoffset >= -BIG && c.instoffset < BIG {
				return C_SAUTO
			}
			return C_LAUTO

		case obj.NAME_PARAM:
			if a.Reg == REGSP {
				// unset base register for better printing, since
				// a.Offset is still relative to pseudo-FP.
				a.Reg = obj.REG_NONE
			}
			c.instoffset = int64(c.autosize) + a.Offset + c.ctxt.Arch.FixedFrameSize
			if c.instoffset >= -BIG && c.instoffset < BIG {
				return C_SAUTO
			}
			return C_LAUTO

		case obj.NAME_NONE:
			if a.Index != 0 {
				if a.Offset != 0 {
					return C_GOK
				}
				// register offset
				return C_ROFF
			}

			c.instoffset = a.Offset
			if c.instoffset == 0 {
				return C_ZOREG
			}
			if c.instoffset >= -BIG && c.instoffset < BIG {
				return C_SOREG
			}
			return C_LOREG

		case obj.NAME_GOTREF:
			return C_GOTADDR
		}

		return C_GOK

	case obj.TYPE_TEXTSIZE:
		return C_TEXTSIZE

	case obj.TYPE_CONST,
		obj.TYPE_ADDR:
		switch a.Name {
		case obj.NAME_NONE:
			c.instoffset = a.Offset
			if a.Reg != 0 {
				if -BIG <= c.instoffset && c.instoffset <= BIG {
					return C_SACON
				}
				if isint32(c.instoffset) {
					return C_LACON
				}
				return C_DACON
			}

		case obj.NAME_EXTERN,
			obj.NAME_STATIC:
			s := a.Sym
			if s == nil {
				return C_GOK
			}

			c.instoffset = a.Offset
			if s.Type == objabi.STLSBSS {
				c.ctxt.Diag("taking address of TLS variable is not supported")
			}
			return C_EXTADDR

		case obj.NAME_AUTO:
			if a.Reg == REGSP {
				// unset base register for better printing, since
				// a.Offset is still relative to pseudo-SP.
				a.Reg = obj.REG_NONE
			}
			c.instoffset = int64(c.autosize) + a.Offset
			if c.instoffset >= -BIG && c.instoffset < BIG {
				return C_SACON
			}
			return C_LACON

		case obj.NAME_PARAM:
			if a.Reg == REGSP {
				// unset base register for better printing, since
				// a.Offset is still relative to pseudo-FP.
				a.Reg = obj.REG_NONE
			}
			c.instoffset = int64(c.autosize) + a.Offset + c.ctxt.Arch.FixedFrameSize
			if c.instoffset >= -BIG && c.instoffset < BIG {
				return C_SACON
			}
			return C_LACON

		default:
			return C_GOK
		}

		if c.instoffset != int64(int32(c.instoffset)) {
			return dconClass(c.instoffset)
		}

		if c.instoffset >= 0 {
			sbits := bits.Len64(uint64(c.instoffset))
			switch {
			case sbits <= 8:
				return C_ZCON + sbits
			case sbits <= 12:
				if c.instoffset <= 0x7ff {
					return C_US12CON
				}
				return C_U12CON
			case sbits <= 13:
				if c.instoffset&0xfff == 0 {
					return C_U13CON20_0
				}
				return C_U13CON
			case sbits <= 15:
				if c.instoffset&0xfff == 0 {
					return C_U15CON20_0
				}
				return C_U15CON
			}
		} else {
			sbits := bits.Len64(uint64(^c.instoffset))
			switch {
			case sbits < 5:
				return C_S5CON
			case sbits < 12:
				return C_S12CON
			case sbits < 13:
				if c.instoffset&0xfff == 0 {
					return C_S13CON20_0
				}
				return C_S13CON
			}
		}

		if c.instoffset&0xfff == 0 {
			return C_32CON20_0
		}
		return C_32CON

	case obj.TYPE_BRANCH:
		return C_BRAN
	}

	return C_GOK
}

// The constants here define the data characteristics within the bit field range.
//
//	ALL1: The data in the bit field is all 1
//	ALL0: The data in the bit field is all 0
//	ST1: The data in the bit field starts with 1, but not all 1
//	ST0: The data in the bit field starts with 0, but not all 0
const (
	ALL1 = iota
	ALL0
	ST1
	ST0
)

// mask returns the mask of the specified bit field, which is used to help determine
// the data characteristics of the immediate value at the specified bit.
func mask(suf int8, len int8) (uint64, uint64) {
	if len == 12 {
		if suf == 0 {
			return 0xfff, 0x800
		} else { // suf == 52
			return 0xfff0000000000000, 0x8000000000000000
		}
	} else { // len == 20
		if suf == 12 {
			return 0xfffff000, 0x80000000
		} else { // suf == 32
			return 0xfffff00000000, 0x8000000000000
		}
	}
}

// bitField return a number represent status of val in bit field
//
//	suf: The starting bit of the bit field
//	len: The length of the bit field
func bitField(val int64, suf int8, len int8) int8 {
	mask1, mask2 := mask(suf, len)
	if uint64(val)&mask1 == mask1 {
		return ALL1
	} else if uint64(val)&mask1 == 0x0 {
		return ALL0
	} else if uint64(val)&mask2 == mask2 {
		return ST1
	} else {
		return ST0
	}
}

// Loading an immediate value larger than 32 bits requires four instructions
// on loong64 (lu12i.w + ori + lu32i.d + lu52i.d), but in some special cases,
// we can use the sign extension and zero extension features of the instruction
// to fill in the high-order data (all 0 or all 1), which can save one to
// three instructions.
//
//	| 63 ~ 52 | 51 ~ 32 | 31 ~ 12 | 11 ~ 0 |
//	| lu52i.d | lu32i.d | lu12i.w |   ori  |
func dconClass(offset int64) int {
	tzb := bits.TrailingZeros64(uint64(offset))
	hi12 := bitField(offset, 52, 12)
	hi20 := bitField(offset, 32, 20)
	lo20 := bitField(offset, 12, 20)
	lo12 := bitField(offset, 0, 12)
	if tzb >= 52 {
		return C_DCON12_0 // lu52i.d
	}
	if tzb >= 32 {
		if ((hi20 == ALL1 || hi20 == ST1) && hi12 == ALL1) || ((hi20 == ALL0 || hi20 == ST0) && hi12 == ALL0) {
			return C_DCON20S_0 // addi.w + lu32i.d
		}
		return C_DCON32_0 // addi.w + lu32i.d + lu52i.d
	}
	if tzb >= 12 {
		if lo20 == ST1 || lo20 == ALL1 {
			if hi20 == ALL1 {
				return C_DCON12_20S // lu12i.w + lu52i.d
			}
			if (hi20 == ST1 && hi12 == ALL1) || ((hi20 == ST0 || hi20 == ALL0) && hi12 == ALL0) {
				return C_DCON20S_20 // lu12i.w + lu32i.d
			}
			return C_DCON32_20 // lu12i.w + lu32i.d + lu52i.d
		}
		if hi20 == ALL0 {
			return C_DCON12_20S // lu12i.w + lu52i.d
		}
		if (hi20 == ST0 && hi12 == ALL0) || ((hi20 == ST1 || hi20 == ALL1) && hi12 == ALL1) {
			return C_DCON20S_20 // lu12i.w + lu32i.d
		}
		return C_DCON32_20 // lu12i.w + lu32i.d + lu52i.d
	}
	if lo12 == ST1 || lo12 == ALL1 {
		if lo20 == ALL1 {
			if hi20 == ALL1 {
				return C_DCON12_12S // addi.d + lu52i.d
			}
			if (hi20 == ST1 && hi12 == ALL1) || ((hi20 == ST0 || hi20 == ALL0) && hi12 == ALL0) {
				return C_DCON20S_12S // addi.w + lu32i.d
			}
			return C_DCON32_12S // addi.w + lu32i.d + lu52i.d
		}
		if lo20 == ST1 {
			if hi20 == ALL1 {

				return C_DCON12_32S // lu12i.w + ori + lu52i.d
			}
			if (hi20 == ST1 && hi12 == ALL1) || ((hi20 == ST0 || hi20 == ALL0) && hi12 == ALL0) {
				return C_DCON20S_32 // lu12i.w + ori + lu32i.d
			}
			return C_DCON // lu12i.w + ori + lu32i.d + lu52i.d
		}
		if lo20 == ALL0 {
			if hi20 == ALL0 {
				return C_DCON12_12U // ori + lu52i.d
			}
			if ((hi20 == ST1 || hi20 == ALL1) && hi12 == ALL1) || (hi20 == ST0 && hi12 == ALL0) {
				return C_DCON20S_12U // ori + lu32i.d
			}
			return C_DCON32_12U // ori + lu32i.d + lu52i.d
		}
		if hi20 == ALL0 {
			return C_DCON12_32S // lu12i.w + ori + lu52i.d
		}
		if ((hi20 == ST1 || hi20 == ALL1) && hi12 == ALL1) || (hi20 == ST0 && hi12 == ALL0) {
			return C_DCON20S_32 // lu12i.w + ori + lu32i.d
		}
		return C_DCON // lu12i.w + ori + lu32i.d + lu52i.d
	}
	if lo20 == ALL0 {
		if hi20 == ALL0 {
			return C_DCON12_12U // ori + lu52i.d
		}
		if ((hi20 == ST1 || hi20 == ALL1) && hi12 == ALL1) || (hi20 == ST0 && hi12 == ALL0) {
			return C_DCON20S_12U // ori + lu32i.d
		}
		return C_DCON32_12U // ori + lu32i.d + lu52i.d
	}
	if lo20 == ST1 || lo20 == ALL1 {
		if hi20 == ALL1 {
			return C_DCON12_32S // lu12i.w + ori + lu52i.d
		}
		if (hi20 == ST1 && hi12 == ALL1) || ((hi20 == ST0 || hi20 == ALL0) && hi12 == ALL0) {
			return C_DCON20S_32 // lu12i.w + ori + lu32i.d
		}
		return C_DCON
	}
	if hi20 == ALL0 {
		return C_DCON12_32S // lu12i.w + ori + lu52i.d
	}
	if ((hi20 == ST1 || hi20 == ALL1) && hi12 == ALL1) || (hi20 == ST0 && hi12 == ALL0) {
		return C_DCON20S_32 // lu12i.w + ori + lu32i.d
	}
	return C_DCON
}

// In Loong64，there are 8 CFRs, denoted as fcc0-fcc7.
// There are 4 FCSRs, denoted as fcsr0-fcsr3.
func (c *ctxt0) rclass(r int16) int {
	switch {
	case REG_R0 <= r && r <= REG_R31:
		return C_REG
	case REG_F0 <= r && r <= REG_F31:
		return C_FREG
	case REG_FCC0 <= r && r <= REG_FCC7:
		return C_FCCREG
	case REG_FCSR0 <= r && r <= REG_FCSR3:
		return C_FCSRREG
	case REG_V0 <= r && r <= REG_V31:
		return C_VREG
	case REG_X0 <= r && r <= REG_X31:
		return C_XREG
	case r >= REG_ARNG && r < REG_ELEM:
		return C_ARNG
	case r >= REG_ELEM && r < REG_ELEM_END:
		return C_ELEM
	}

	return C_GOK
}

func oclass(a *obj.Addr) int {
	return int(a.Class) - 1
}

func prasm(p *obj.Prog) {
	fmt.Printf("%v\n", p)
}

func (c *ctxt0) oplook(p *obj.Prog) *Optab {
	if oprange[AOR&obj.AMask] == nil {
		c.ctxt.Diag("loong64 ops not initialized, call loong64.buildop first")
	}

	a1 := int(p.Optab)
	if a1 != 0 {
		return &optab[a1-1]
	}

	// first source operand
	a1 = int(p.From.Class)
	if a1 == 0 {
		a1 = c.aclass(&p.From) + 1
		p.From.Class = int8(a1)
	}
	a1--

	// first destination operand
	a4 := int(p.To.Class)
	if a4 == 0 {
		a4 = c.aclass(&p.To) + 1
		p.To.Class = int8(a4)
	}
	a4--

	// 2nd source operand
	a2 := C_NONE
	if p.Reg != 0 {
		a2 = c.rclass(p.Reg)
	}

	// 2nd destination operand
	a5 := C_NONE
	if p.RegTo2 != 0 {
		a5 = C_REG
	}

	// 3rd source operand
	a3 := C_NONE
	if len(p.RestArgs) > 0 {
		a3 = int(p.RestArgs[0].Class)
		if a3 == 0 {
			a3 = c.aclass(&p.RestArgs[0].Addr) + 1
			p.RestArgs[0].Class = int8(a3)
		}
		a3--
	}

	ops := oprange[p.As&obj.AMask]
	c1 := &xcmp[a1]
	c3 := &xcmp[a3]
	c4 := &xcmp[a4]
	for i := range ops {
		op := &ops[i]
		if (int(op.reg) == a2) && c3[op.from3] && c1[op.from1] && c4[op.to1] && (int(op.to2) == a5) {
			p.Optab = uint16(cap(optab) - cap(ops) + i + 1)
			return op
		}
	}

	c.ctxt.Diag("illegal combination %v %v %v %v %v %v", p.As, DRconv(a1), DRconv(a2), DRconv(a3), DRconv(a4), DRconv(a5))
	prasm(p)
	// Turn illegal instruction into an UNDEF, avoid crashing in asmout.
	return &Optab{obj.AUNDEF, C_NONE, C_NONE, C_NONE, C_NONE, C_NONE, 49, 4, 0, 0}
}

func cmp(a int, b int) bool {
	if a == b {
		return true
	}
	switch a {
	case C_DCON:
		return cmp(C_32CON, b) || cmp(C_DCON12_20S, b) || cmp(C_DCON32_12S, b) || b == C_DCON12_0
	case C_32CON:
		return cmp(C_32CON20_0, b) || cmp(C_U15CON, b) || cmp(C_13CON, b) || cmp(C_12CON, b)
	case C_32CON20_0:
		return b == C_U15CON20_0 || b == C_U13CON20_0 || b == C_S13CON20_0 || b == C_ZCON
	case C_U15CON:
		return cmp(C_U12CON, b) || b == C_U15CON20_0 || b == C_U13CON20_0 || b == C_U13CON
	case C_13CON:
		return cmp(C_U13CON, b) || cmp(C_S13CON, b)
	case C_U13CON:
		return cmp(C_12CON, b) || b == C_U13CON20_0
	case C_S13CON:
		return cmp(C_12CON, b) || b == C_S13CON20_0
	case C_12CON:
		return cmp(C_U12CON, b) || cmp(C_S12CON, b)
	case C_UU12CON:
		return cmp(C_U12CON, b)
	case C_U12CON:
		return cmp(C_U8CON, b) || b == C_US12CON
	case C_U8CON:
		return cmp(C_U7CON, b)
	case C_U7CON:
		return cmp(C_U6CON, b)
	case C_U6CON:
		return cmp(C_U5CON, b)
	case C_U5CON:
		return cmp(C_U4CON, b)
	case C_U4CON:
		return cmp(C_U3CON, b)
	case C_U3CON:
		return cmp(C_U2CON, b)
	case C_U2CON:
		return cmp(C_U1CON, b)
	case C_U1CON:
		return cmp(C_ZCON, b)
	case C_US12CON:
		return cmp(C_S12CON, b)
	case C_S12CON:
		return cmp(C_S5CON, b) || cmp(C_U8CON, b) || b == C_US12CON
	case C_S5CON:
		return cmp(C_ZCON, b) || cmp(C_U4CON, b)

	case C_DCON12_20S:
		if b == C_DCON20S_20 || b == C_DCON12_12S ||
			b == C_DCON20S_12S || b == C_DCON12_12U ||
			b == C_DCON20S_12U || b == C_DCON20S_0 {
			return true
		}

	case C_DCON32_12S:
		if b == C_DCON32_20 || b == C_DCON12_32S ||
			b == C_DCON20S_32 || b == C_DCON32_12U ||
			b == C_DCON32_0 {
			return true
		}

	case C_LACON:
		return b == C_SACON

	case C_LAUTO:
		return b == C_SAUTO

	case C_REG:
		return b == C_ZCON

	case C_LOREG:
		return b == C_ZOREG || b == C_SOREG

	case C_SOREG:
		return b == C_ZOREG
	}

	return false
}

func ocmp(p1, p2 Optab) int {
	if p1.as != p2.as {
		return int(p1.as) - int(p2.as)
	}
	if p1.from1 != p2.from1 {
		return int(p1.from1) - int(p2.from1)
	}
	if p1.reg != p2.reg {
		return int(p1.reg) - int(p2.reg)
	}
	if p1.to1 != p2.to1 {
		return int(p1.to1) - int(p2.to1)
	}
	return 0
}

func opset(a, b0 obj.As) {
	oprange[a&obj.AMask] = oprange[b0]
}

func buildop(ctxt *obj.Link) {
	if ctxt.DiagFunc == nil {
		ctxt.DiagFunc = func(format string, args ...interface{}) {
			log.Printf(format, args...)
		}
	}

	if oprange[AOR&obj.AMask] != nil {
		// Already initialized; stop now.
		// This happens in the cmd/asm tests,
		// each of which re-initializes the arch.
		return
	}

	var n int

	for i := 0; i < C_NCLASS; i++ {
		for n = 0; n < C_NCLASS; n++ {
			if cmp(n, i) {
				xcmp[i][n] = true
			}
		}
	}
	for n = 0; optab[n].as != obj.AXXX; n++ {
	}
	slices.SortFunc(optab[:n], ocmp)
	for i := 0; i < n; i++ {
		r := optab[i].as
		r0 := r & obj.AMask
		start := i
		for optab[i].as == r {
			i++
		}
		oprange[r0] = optab[start:i]
		i--

		switch r {
		default:
			ctxt.Diag("unknown op in build: %v", r)
			ctxt.DiagFlush()
			log.Fatalf("bad code")

		case AABSF:
			opset(AMOVFD, r0)
			opset(AMOVDF, r0)
			opset(AMOVWF, r0)
			opset(AMOVFW, r0)
			opset(AMOVWD, r0)
			opset(AMOVDW, r0)
			opset(ANEGF, r0)
			opset(ANEGD, r0)
			opset(AABSD, r0)
			opset(ATRUNCDW, r0)
			opset(ATRUNCFW, r0)
			opset(ASQRTF, r0)
			opset(ASQRTD, r0)
			opset(AFCLASSF, r0)
			opset(AFCLASSD, r0)
			opset(AFLOGBF, r0)
			opset(AFLOGBD, r0)

		case AMOVVF:
			opset(AMOVVD, r0)
			opset(AMOVFV, r0)
			opset(AMOVDV, r0)
			opset(ATRUNCDV, r0)
			opset(ATRUNCFV, r0)
			opset(AFFINTFW, r0)
			opset(AFFINTFV, r0)
			opset(AFFINTDW, r0)
			opset(AFFINTDV, r0)
			opset(AFTINTWF, r0)
			opset(AFTINTWD, r0)
			opset(AFTINTVF, r0)
			opset(AFTINTVD, r0)
			opset(AFTINTRPWF, r0)
			opset(AFTINTRPWD, r0)
			opset(AFTINTRPVF, r0)
			opset(AFTINTRPVD, r0)
			opset(AFTINTRMWF, r0)
			opset(AFTINTRMWD, r0)
			opset(AFTINTRMVF, r0)
			opset(AFTINTRMVD, r0)
			opset(AFTINTRZWF, r0)
			opset(AFTINTRZWD, r0)
			opset(AFTINTRZVF, r0)
			opset(AFTINTRZVD, r0)
			opset(AFTINTRNEWF, r0)
			opset(AFTINTRNEWD, r0)
			opset(AFTINTRNEVF, r0)
			opset(AFTINTRNEVD, r0)

		case AADD:
			opset(ASGT, r0)
			opset(ASGTU, r0)
			opset(AADDU, r0)

		case AADDV:
			opset(AADDVU, r0)

		case AADDF:
			opset(ADIVF, r0)
			opset(ADIVD, r0)
			opset(AMULF, r0)
			opset(AMULD, r0)
			opset(ASUBF, r0)
			opset(ASUBD, r0)
			opset(AADDD, r0)
			opset(AFMINF, r0)
			opset(AFMIND, r0)
			opset(AFMAXF, r0)
			opset(AFMAXD, r0)
			opset(AFCOPYSGF, r0)
			opset(AFCOPYSGD, r0)
			opset(AFSCALEBF, r0)
			opset(AFSCALEBD, r0)
			opset(AFMAXAF, r0)
			opset(AFMAXAD, r0)
			opset(AFMINAF, r0)
			opset(AFMINAD, r0)

		case AFMADDF:
			opset(AFMADDD, r0)
			opset(AFMSUBF, r0)
			opset(AFMSUBD, r0)
			opset(AFNMADDF, r0)
			opset(AFNMADDD, r0)
			opset(AFNMSUBF, r0)
			opset(AFNMSUBD, r0)

		case AAND:
			opset(AOR, r0)
			opset(AXOR, r0)
			opset(AORN, r0)
			opset(AANDN, r0)

		case ABEQ:
			opset(ABNE, r0)
			opset(ABLT, r0)
			opset(ABGE, r0)
			opset(ABGEU, r0)
			opset(ABLTU, r0)

		case ABLEZ:
			opset(ABGEZ, r0)
			opset(ABLTZ, r0)
			opset(ABGTZ, r0)

		case AMOVB:
			opset(AMOVH, r0)

		case AMOVBU:
			opset(AMOVHU, r0)

		case AMUL:
			opset(AMULU, r0)
			opset(AMULH, r0)
			opset(AMULHU, r0)
			opset(AREM, r0)
			opset(AREMU, r0)
			opset(ADIV, r0)
			opset(ADIVU, r0)

		case AMULV:
			opset(AMULVU, r0)
			opset(AMULHV, r0)
			opset(AMULHVU, r0)
			opset(AREMV, r0)
			opset(AREMVU, r0)
			opset(ADIVV, r0)
			opset(ADIVVU, r0)

		case ASLL:
			opset(ASRL, r0)
			opset(ASRA, r0)
			opset(AROTR, r0)

		case ASLLV:
			opset(ASRAV, r0)
			opset(ASRLV, r0)
			opset(AROTRV, r0)

		case ABSTRPICKW:
			opset(ABSTRPICKV, r0)
			opset(ABSTRINSW, r0)
			opset(ABSTRINSV, r0)

		case ASUB:
			opset(ASUBU, r0)
			opset(ANOR, r0)

		case ASUBV:
			opset(ASUBVU, r0)

		case ASYSCALL:
			opset(ADBAR, r0)
			opset(ABREAK, r0)

		case ACMPEQF:
			opset(ACMPGTF, r0)
			opset(ACMPGTD, r0)
			opset(ACMPGEF, r0)
			opset(ACMPGED, r0)
			opset(ACMPEQD, r0)

		case ABFPT:
			opset(ABFPF, r0)

		case AMOVW,
			AMOVD,
			AMOVF,
			AMOVV,
			ARFE,
			AJAL,
			AJMP,
			AMOVWU,
			AVMOVQ,
			AXVMOVQ,
			ALL,
			ALLV,
			ASC,
			ASCV,
			ANEGW,
			ANEGV,
			AWORD,
			APRELD,
			APRELDX,
			obj.ANOP,
			obj.ATEXT,
			obj.AFUNCDATA,
			obj.APCALIGN,
			obj.APCDATA,
			obj.ADUFFZERO,
			obj.ADUFFCOPY:
			break

		case ARDTIMELW:
			opset(ARDTIMEHW, r0)
			opset(ARDTIMED, r0)

		case ACLOW:
			opset(ACLZW, r0)
			opset(ACTOW, r0)
			opset(ACTZW, r0)
			opset(ACLOV, r0)
			opset(ACLZV, r0)
			opset(ACTOV, r0)
			opset(ACTZV, r0)
			opset(AREVB2H, r0)
			opset(AREVB4H, r0)
			opset(AREVB2W, r0)
			opset(AREVBV, r0)
			opset(AREVH2W, r0)
			opset(AREVHV, r0)
			opset(ABITREV4B, r0)
			opset(ABITREV8B, r0)
			opset(ABITREVW, r0)
			opset(ABITREVV, r0)
			opset(AEXTWB, r0)
			opset(AEXTWH, r0)
			opset(ACPUCFG, r0)

		case ATEQ:
			opset(ATNE, r0)

		case AMASKEQZ:
			opset(AMASKNEZ, r0)
			opset(ACRCWBW, r0)
			opset(ACRCWHW, r0)
			opset(ACRCWWW, r0)
			opset(ACRCWVW, r0)
			opset(ACRCCWBW, r0)
			opset(ACRCCWHW, r0)
			opset(ACRCCWWW, r0)
			opset(ACRCCWVW, r0)

		case ANOOP:
			opset(obj.AUNDEF, r0)

		case AAMSWAPW:
			for i := range atomicInst {
				if i == AAMSWAPW {
					continue
				}
				opset(i, r0)
			}

		case AVSEQB:
			opset(AVSEQH, r0)
			opset(AVSEQW, r0)
			opset(AVSEQV, r0)
			opset(AVILVLB, r0)
			opset(AVILVLH, r0)
			opset(AVILVLW, r0)
			opset(AVILVLV, r0)
			opset(AVILVHB, r0)
			opset(AVILVHH, r0)
			opset(AVILVHW, r0)
			opset(AVILVHV, r0)
			opset(AVMULB, r0)
			opset(AVMULH, r0)
			opset(AVMULW, r0)
			opset(AVMULV, r0)
			opset(AVMUHB, r0)
			opset(AVMUHH, r0)
			opset(AVMUHW, r0)
			opset(AVMUHV, r0)
			opset(AVMUHBU, r0)
			opset(AVMUHHU, r0)
			opset(AVMUHWU, r0)
			opset(AVMUHVU, r0)
			opset(AVDIVB, r0)
			opset(AVDIVH, r0)
			opset(AVDIVW, r0)
			opset(AVDIVV, r0)
			opset(AVMODB, r0)
			opset(AVMODH, r0)
			opset(AVMODW, r0)
			opset(AVMODV, r0)
			opset(AVDIVBU, r0)
			opset(AVDIVHU, r0)
			opset(AVDIVWU, r0)
			opset(AVDIVVU, r0)
			opset(AVMODBU, r0)
			opset(AVMODHU, r0)
			opset(AVMODWU, r0)
			opset(AVMODVU, r0)
			opset(AVMULWEVHB, r0)
			opset(AVMULWEVWH, r0)
			opset(AVMULWEVVW, r0)
			opset(AVMULWEVQV, r0)
			opset(AVMULWODHB, r0)
			opset(AVMULWODWH, r0)
			opset(AVMULWODVW, r0)
			opset(AVMULWODQV, r0)
			opset(AVMULWEVHBU, r0)
			opset(AVMULWEVWHU, r0)
			opset(AVMULWEVVWU, r0)
			opset(AVMULWEVQVU, r0)
			opset(AVMULWODHBU, r0)
			opset(AVMULWODWHU, r0)
			opset(AVMULWODVWU, r0)
			opset(AVMULWODQVU, r0)
			opset(AVMULWEVHBUB, r0)
			opset(AVMULWEVWHUH, r0)
			opset(AVMULWEVVWUW, r0)
			opset(AVMULWEVQVUV, r0)
			opset(AVMULWODHBUB, r0)
			opset(AVMULWODWHUH, r0)
			opset(AVMULWODVWUW, r0)
			opset(AVMULWODQVUV, r0)
			opset(AVADDF, r0)
			opset(AVADDD, r0)
			opset(AVSUBF, r0)
			opset(AVSUBD, r0)
			opset(AVMULF, r0)
			opset(AVMULD, r0)
			opset(AVDIVF, r0)
			opset(AVDIVD, r0)

		case AXVSEQB:
			opset(AXVSEQH, r0)
			opset(AXVSEQW, r0)
			opset(AXVSEQV, r0)
			opset(AXVILVLB, r0)
			opset(AXVILVLH, r0)
			opset(AXVILVLW, r0)
			opset(AXVILVLV, r0)
			opset(AXVILVHB, r0)
			opset(AXVILVHH, r0)
			opset(AXVILVHW, r0)
			opset(AXVILVHV, r0)
			opset(AXVMULB, r0)
			opset(AXVMULH, r0)
			opset(AXVMULW, r0)
			opset(AXVMULV, r0)
			opset(AXVMUHB, r0)
			opset(AXVMUHH, r0)
			opset(AXVMUHW, r0)
			opset(AXVMUHV, r0)
			opset(AXVMUHBU, r0)
			opset(AXVMUHHU, r0)
			opset(AXVMUHWU, r0)
			opset(AXVMUHVU, r0)
			opset(AXVDIVB, r0)
			opset(AXVDIVH, r0)
			opset(AXVDIVW, r0)
			opset(AXVDIVV, r0)
			opset(AXVMODB, r0)
			opset(AXVMODH, r0)
			opset(AXVMODW, r0)
			opset(AXVMODV, r0)
			opset(AXVDIVBU, r0)
			opset(AXVDIVHU, r0)
			opset(AXVDIVWU, r0)
			opset(AXVDIVVU, r0)
			opset(AXVMODBU, r0)
			opset(AXVMODHU, r0)
			opset(AXVMODWU, r0)
			opset(AXVMODVU, r0)
			opset(AXVMULWEVHB, r0)
			opset(AXVMULWEVWH, r0)
			opset(AXVMULWEVVW, r0)
			opset(AXVMULWEVQV, r0)
			opset(AXVMULWODHB, r0)
			opset(AXVMULWODWH, r0)
			opset(AXVMULWODVW, r0)
			opset(AXVMULWODQV, r0)
			opset(AXVMULWEVHBU, r0)
			opset(AXVMULWEVWHU, r0)
			opset(AXVMULWEVVWU, r0)
			opset(AXVMULWEVQVU, r0)
			opset(AXVMULWODHBU, r0)
			opset(AXVMULWODWHU, r0)
			opset(AXVMULWODVWU, r0)
			opset(AXVMULWODQVU, r0)
			opset(AXVMULWEVHBUB, r0)
			opset(AXVMULWEVWHUH, r0)
			opset(AXVMULWEVVWUW, r0)
			opset(AXVMULWEVQVUV, r0)
			opset(AXVMULWODHBUB, r0)
			opset(AXVMULWODWHUH, r0)
			opset(AXVMULWODVWUW, r0)
			opset(AXVMULWODQVUV, r0)
			opset(AXVADDF, r0)
			opset(AXVADDD, r0)
			opset(AXVSUBF, r0)
			opset(AXVSUBD, r0)
			opset(AXVMULF, r0)
			opset(AXVMULD, r0)
			opset(AXVDIVF, r0)
			opset(AXVDIVD, r0)

		case AVANDB:
			opset(AVORB, r0)
			opset(AVXORB, r0)
			opset(AVNORB, r0)
			opset(AVSHUF4IB, r0)
			opset(AVSHUF4IH, r0)
			opset(AVSHUF4IW, r0)
			opset(AVSHUF4IV, r0)

		case AXVANDB:
			opset(AXVORB, r0)
			opset(AXVXORB, r0)
			opset(AXVNORB, r0)
			opset(AXVSHUF4IB, r0)
			opset(AXVSHUF4IH, r0)
			opset(AXVSHUF4IW, r0)
			opset(AXVSHUF4IV, r0)

		case AVANDV:
			opset(AVORV, r0)
			opset(AVXORV, r0)
			opset(AVNORV, r0)
			opset(AVANDNV, r0)
			opset(AVORNV, r0)

		case AXVANDV:
			opset(AXVORV, r0)
			opset(AXVXORV, r0)
			opset(AXVNORV, r0)
			opset(AXVANDNV, r0)
			opset(AXVORNV, r0)

		case AVPCNTB:
			opset(AVPCNTH, r0)
			opset(AVPCNTW, r0)
			opset(AVPCNTV, r0)
			opset(AVFSQRTF, r0)
			opset(AVFSQRTD, r0)
			opset(AVFRECIPF, r0)
			opset(AVFRECIPD, r0)
			opset(AVFRSQRTF, r0)
			opset(AVFRSQRTD, r0)
			opset(AVNEGB, r0)
			opset(AVNEGH, r0)
			opset(AVNEGW, r0)
			opset(AVNEGV, r0)
			opset(AVFRINTRNEF, r0)
			opset(AVFRINTRNED, r0)
			opset(AVFRINTRZF, r0)
			opset(AVFRINTRZD, r0)
			opset(AVFRINTRPF, r0)
			opset(AVFRINTRPD, r0)
			opset(AVFRINTRMF, r0)
			opset(AVFRINTRMD, r0)
			opset(AVFRINTF, r0)
			opset(AVFRINTD, r0)
			opset(AVFCLASSF, r0)
			opset(AVFCLASSD, r0)

		case AXVPCNTB:
			opset(AXVPCNTH, r0)
			opset(AXVPCNTW, r0)
			opset(AXVPCNTV, r0)
			opset(AXVFSQRTF, r0)
			opset(AXVFSQRTD, r0)
			opset(AXVFRECIPF, r0)
			opset(AXVFRECIPD, r0)
			opset(AXVFRSQRTF, r0)
			opset(AXVFRSQRTD, r0)
			opset(AXVNEGB, r0)
			opset(AXVNEGH, r0)
			opset(AXVNEGW, r0)
			opset(AXVNEGV, r0)
			opset(AXVFRINTRNEF, r0)
			opset(AXVFRINTRNED, r0)
			opset(AXVFRINTRZF, r0)
			opset(AXVFRINTRZD, r0)
			opset(AXVFRINTRPF, r0)
			opset(AXVFRINTRPD, r0)
			opset(AXVFRINTRMF, r0)
			opset(AXVFRINTRMD, r0)
			opset(AXVFRINTF, r0)
			opset(AXVFRINTD, r0)
			opset(AXVFCLASSF, r0)
			opset(AXVFCLASSD, r0)

		case AVADDB:
			opset(AVADDH, r0)
			opset(AVADDW, r0)
			opset(AVADDV, r0)
			opset(AVADDQ, r0)
			opset(AVSUBB, r0)
			opset(AVSUBH, r0)
			opset(AVSUBW, r0)
			opset(AVSUBV, r0)
			opset(AVSUBQ, r0)

		case AXVADDB:
			opset(AXVADDH, r0)
			opset(AXVADDW, r0)
			opset(AXVADDV, r0)
			opset(AXVADDQ, r0)
			opset(AXVSUBB, r0)
			opset(AXVSUBH, r0)
			opset(AXVSUBW, r0)
			opset(AXVSUBV, r0)
			opset(AXVSUBQ, r0)

		case AVSLLB:
			opset(AVSRLB, r0)
			opset(AVSRAB, r0)
			opset(AVROTRB, r0)

		case AXVSLLB:
			opset(AXVSRLB, r0)
			opset(AXVSRAB, r0)
			opset(AXVROTRB, r0)

		case AVSLLH:
			opset(AVSRLH, r0)
			opset(AVSRAH, r0)
			opset(AVROTRH, r0)

		case AXVSLLH:
			opset(AXVSRLH, r0)
			opset(AXVSRAH, r0)
			opset(AXVROTRH, r0)

		case AVSLLW:
			opset(AVSRLW, r0)
			opset(AVSRAW, r0)
			opset(AVROTRW, r0)
			opset(AVADDBU, r0)
			opset(AVADDHU, r0)
			opset(AVADDWU, r0)
			opset(AVADDVU, r0)
			opset(AVSUBBU, r0)
			opset(AVSUBHU, r0)
			opset(AVSUBWU, r0)
			opset(AVSUBVU, r0)

		case AXVSLLW:
			opset(AXVSRLW, r0)
			opset(AXVSRAW, r0)
			opset(AXVROTRW, r0)
			opset(AXVADDBU, r0)
			opset(AXVADDHU, r0)
			opset(AXVADDWU, r0)
			opset(AXVADDVU, r0)
			opset(AXVSUBBU, r0)
			opset(AXVSUBHU, r0)
			opset(AXVSUBWU, r0)
			opset(AXVSUBVU, r0)

		case AVSLLV:
			opset(AVSRLV, r0)
			opset(AVSRAV, r0)
			opset(AVROTRV, r0)

		case AXVSLLV:
			opset(AXVSRLV, r0)
			opset(AXVSRAV, r0)
			opset(AXVROTRV, r0)

		case AVSETEQV:
			opset(AVSETNEV, r0)
			opset(AVSETANYEQB, r0)
			opset(AVSETANYEQH, r0)
			opset(AVSETANYEQW, r0)
			opset(AVSETANYEQV, r0)
			opset(AVSETALLNEB, r0)
			opset(AVSETALLNEH, r0)
			opset(AVSETALLNEW, r0)
			opset(AVSETALLNEV, r0)

		case AXVSETEQV:
			opset(AXVSETNEV, r0)
			opset(AXVSETANYEQB, r0)
			opset(AXVSETANYEQH, r0)
			opset(AXVSETANYEQW, r0)
			opset(AXVSETANYEQV, r0)
			opset(AXVSETALLNEB, r0)
			opset(AXVSETALLNEH, r0)
			opset(AXVSETALLNEW, r0)
			opset(AXVSETALLNEV, r0)

		}
	}
}

func OP_RRRR(op uint32, r1 uint32, r2 uint32, r3 uint32, r4 uint32) uint32 {
	return op | (r1&0x1F)<<15 | (r2&0x1F)<<10 | (r3&0x1F)<<5 | (r4 & 0x1F)
}

// r1 -> rk
// r2 -> rj
// r3 -> rd
func OP_RRR(op uint32, r1 uint32, r2 uint32, r3 uint32) uint32 {
	return op | (r1&0x1F)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

// r2 -> rj
// r3 -> rd
func OP_RR(op uint32, r2 uint32, r3 uint32) uint32 {
	return op | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_16IR_5I(op uint32, i uint32, r2 uint32) uint32 {
	return op | (i&0xFFFF)<<10 | (r2&0x1F)<<5 | ((i >> 16) & 0x1F)
}

func OP_16IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0xFFFF)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_12IR_5I(op uint32, i1 uint32, r2 uint32, i2 uint32) uint32 {
	return op | (i1&0xFFF)<<10 | (r2&0x1F)<<5 | (i2&0x1F)<<0
}

func OP_12IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0xFFF)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_8IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0xFF)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_6IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0x3F)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_5IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0x1F)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_4IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0xF)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_3IRR(op uint32, i uint32, r2 uint32, r3 uint32) uint32 {
	return op | (i&0x7)<<10 | (r2&0x1F)<<5 | (r3&0x1F)<<0
}

func OP_IR(op uint32, i uint32, r2 uint32) uint32 {
	return op | (i&0xFFFFF)<<5 | (r2&0x1F)<<0 // ui20, rd5
}

func OP_15I(op uint32, i uint32) uint32 {
	return op | (i&0x7FFF)<<0
}

// i1 -> msb
// r2 -> rj
// i3 -> lsb
// r4 -> rd
func OP_IRIR(op uint32, i1 uint32, r2 uint32, i3 uint32, r4 uint32) uint32 {
	return op | (i1 << 16) | (r2&0x1F)<<5 | (i3 << 10) | (r4&0x1F)<<0
}

// Encoding for the 'b' or 'bl' instruction.
func OP_B_BL(op uint32, i uint32) uint32 {
	return op | ((i & 0xFFFF) << 10) | ((i >> 16) & 0x3FF)
}

func (c *ctxt0) asmout(p *obj.Prog, o *Optab, out []uint32) {
	o1 := uint32(0)
	o2 := uint32(0)
	o3 := uint32(0)
	o4 := uint32(0)
	o5 := uint32(0)

	add := AADDU
	add = AADDVU

	switch o.type_ {
	default:
		c.ctxt.Diag("unknown type %d %v", o.type_)
		prasm(p)

	case 0: // pseudo ops
		break

	case 1: // mov r1,r2 ==> OR r1,r0,r2
		a := AOR
		if p.As == AMOVW {
			a = ASLL
		}
		o1 = OP_RRR(c.oprrr(a), uint32(REGZERO), uint32(p.From.Reg), uint32(p.To.Reg))

	case 2: // add/sub r1,[r2],r3
		r := int(p.Reg)
		if p.As == ANEGW || p.As == ANEGV {
			r = REGZERO
		}
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_RRR(c.oprrr(p.As), uint32(p.From.Reg), uint32(r), uint32(p.To.Reg))

	case 3: // mov $soreg, r ==> or/add $i,o,r
		v := c.regoff(&p.From)

		r := int(p.From.Reg)
		if r == 0 {
			r = int(o.param)
		}
		a := add
		if o.from1 == C_12CON && v > 0 {
			a = AOR
		}

		o1 = OP_12IRR(c.opirr(a), uint32(v), uint32(r), uint32(p.To.Reg))

	case 4: // add $scon,[r1],r2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_12IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 5: // syscall
		v := c.regoff(&p.From)
		o1 = OP_15I(c.opi(p.As), uint32(v))

	case 6: // beq r1,[r2],sbra
		v := int32(0)
		if p.To.Target() != nil {
			v = int32(p.To.Target().Pc-p.Pc) >> 2
		}
		as, rd, rj, width := p.As, p.Reg, p.From.Reg, 16
		switch as {
		case ABGTZ, ABLEZ:
			rd, rj = rj, rd
		case ABFPT, ABFPF:
			width = 21
			// FCC0 is the implicit source operand, now that we
			// don't register-allocate from the FCC bank.
			if rj == 0 {
				rj = REG_FCC0
			}
		case ABEQ, ABNE:
			if rd == 0 || rd == REGZERO || rj == REGZERO {
				// BEQZ/BNEZ can be encoded with 21-bit offsets.
				width = 21
				as = -as
				if rj == 0 || rj == REGZERO {
					rj = rd
				}
			}
		}
		switch width {
		case 21:
			if (v<<11)>>11 != v {
				c.ctxt.Diag("21 bit-width, short branch too far\n%v", p)
			}
			o1 = OP_16IR_5I(c.opirr(as), uint32(v), uint32(rj))
		case 16:
			if (v<<16)>>16 != v {
				c.ctxt.Diag("16 bit-width, short branch too far\n%v", p)
			}
			o1 = OP_16IRR(c.opirr(as), uint32(v), uint32(rj), uint32(rd))
		default:
			c.ctxt.Diag("unexpected branch encoding\n%v", p)
		}

	case 7: // mov r, soreg
		r := int(p.To.Reg)
		if r == 0 {
			r = int(o.param)
		}
		v := c.regoff(&p.To)
		o1 = OP_12IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.From.Reg))

	case 8: // mov soreg, r
		r := int(p.From.Reg)
		if r == 0 {
			r = int(o.param)
		}
		v := c.regoff(&p.From)
		o1 = OP_12IRR(c.opirr(-p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 9: // sll r1,[r2],r3
		o1 = OP_RR(c.oprr(p.As), uint32(p.From.Reg), uint32(p.To.Reg))

	case 10: // add $con,[r1],r2 ==> mov $con, t; add t,[r1],r2
		v := c.regoff(&p.From)
		a := AOR
		if v < 0 {
			a = AADDU
		}
		o1 = OP_12IRR(c.opirr(a), uint32(v), uint32(0), uint32(REGTMP))
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o2 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 11: // jmp lbra
		v := int32(0)
		if p.To.Target() != nil {
			v = int32(p.To.Target().Pc-p.Pc) >> 2
		}
		o1 = OP_B_BL(c.opirr(p.As), uint32(v))
		if p.To.Sym != nil {
			c.cursym.AddRel(c.ctxt, obj.Reloc{
				Type: objabi.R_CALLLOONG64,
				Off:  int32(c.pc),
				Siz:  4,
				Sym:  p.To.Sym,
				Add:  p.To.Offset,
			})
		}

	case 12: // movbs r,r
		switch p.As {
		case AMOVB:
			o1 = OP_RR(c.oprr(AEXTWB), uint32(p.From.Reg), uint32(p.To.Reg))
		case AMOVH:
			o1 = OP_RR(c.oprr(AEXTWH), uint32(p.From.Reg), uint32(p.To.Reg))
		case AMOVBU:
			o1 = OP_12IRR(c.opirr(AAND), uint32(0xff), uint32(p.From.Reg), uint32(p.To.Reg))
		case AMOVHU:
			o1 = OP_IRIR(c.opirir(ABSTRPICKV), 15, uint32(p.From.Reg), 0, uint32(p.To.Reg))
		case AMOVWU:
			o1 = OP_IRIR(c.opirir(ABSTRPICKV), 31, uint32(p.From.Reg), 0, uint32(p.To.Reg))
		default:
			c.ctxt.Diag("unexpected encoding\n%v", p)
		}

	case 13: // vsll $ui3, [vr1], vr2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_3IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 14: // vsll $ui4, [vr1], vr2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_4IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 15: // teq $c r,r
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = REGZERO
		}
		/*
			teq c, r1, r2
			fallthrough
			==>
			bne r1, r2, 2
			break c
			fallthrough
		*/
		if p.As == ATEQ {
			o1 = OP_16IRR(c.opirr(ABNE), uint32(2), uint32(r), uint32(p.To.Reg))
		} else { // ATNE
			o1 = OP_16IRR(c.opirr(ABEQ), uint32(2), uint32(r), uint32(p.To.Reg))
		}
		o2 = OP_15I(c.opi(ABREAK), uint32(v))

	case 16: // sll $c,[r1],r2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}

		// instruction ending with V:6-digit immediate, others:5-digit immediate
		if v >= 32 && vshift(p.As) {
			o1 = OP_16IRR(c.opirr(p.As), uint32(v)&0x3f, uint32(r), uint32(p.To.Reg))
		} else {
			o1 = OP_16IRR(c.opirr(p.As), uint32(v)&0x1f, uint32(r), uint32(p.To.Reg))
		}

	case 17: // bstrpickw $msbw, r1, $lsbw, r2
		rd, rj := p.To.Reg, p.Reg
		if rj == obj.REG_NONE {
			rj = rd
		}
		msb, lsb := p.From.Offset, p.GetFrom3().Offset

		// check the range of msb and lsb
		var b uint32
		if p.As == ABSTRPICKW || p.As == ABSTRINSW {
			b = 32
		} else {
			b = 64
		}
		if lsb < 0 || uint32(lsb) >= b || msb < 0 || uint32(msb) >= b || uint32(lsb) > uint32(msb) {
			c.ctxt.Diag("illegal bit number\n%v", p)
		}

		o1 = OP_IRIR(c.opirir(p.As), uint32(msb), uint32(rj), uint32(lsb), uint32(rd))

	case 18: // jmp [r1],0(r2)
		r := int(p.Reg)
		if r == 0 {
			r = int(o.param)
		}
		o1 = OP_RRR(c.oprrr(p.As), uint32(0), uint32(p.To.Reg), uint32(r))
		if p.As == obj.ACALL {
			c.cursym.AddRel(c.ctxt, obj.Reloc{
				Type: objabi.R_CALLIND,
				Off:  int32(c.pc),
			})
		}

	case 19: // mov $lcon,r
		// NOTE: this case does not use REGTMP. If it ever does,
		// remove the NOTUSETMP flag in optab.
		v := c.regoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
		o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(p.To.Reg), uint32(p.To.Reg))

	case 20: // mov Rsrc, (Rbase)(Roff)
		o1 = OP_RRR(c.oprrr(p.As), uint32(p.To.Index), uint32(p.To.Reg), uint32(p.From.Reg))

	case 21: // mov (Rbase)(Roff), Rdst
		o1 = OP_RRR(c.oprrr(-p.As), uint32(p.From.Index), uint32(p.From.Reg), uint32(p.To.Reg))

	case 22: // add $si5,[r1],r2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}

		o1 = OP_5IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 23: // add $ui8,[r1],r2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}

		// the operand range available for instructions VSHUF4IV and XVSHUF4IV is [0, 15]
		if p.As == AVSHUF4IV || p.As == AXVSHUF4IV {
			operand := uint32(v)
			c.checkoperand(p, operand, 15)
		}

		o1 = OP_8IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 24: // add $lcon,r1,r2
		v := c.regoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
		o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(REGTMP), uint32(REGTMP))
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o3 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 25: // mov $ucon,r
		v := c.regoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))

	case 26: // add/and $ucon,[r1],r2
		v := c.regoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o2 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 27: // mov $lsext/auto/oreg,r
		v := c.regoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
		o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(REGTMP), uint32(REGTMP))
		r := int(p.From.Reg)
		if r == 0 {
			r = int(o.param)
		}
		o3 = OP_RRR(c.oprrr(add), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 28: // mov [sl]ext/auto/oreg,fr
		v := c.regoff(&p.From)
		r := int(p.From.Reg)
		if r == 0 {
			r = int(o.param)
		}
		switch o.size {
		case 12:
			o1 = OP_IR(c.opir(ALU12IW), uint32((v+1<<11)>>12), uint32(REGTMP))
			o2 = OP_RRR(c.oprrr(add), uint32(r), uint32(REGTMP), uint32(REGTMP))
			o3 = OP_12IRR(c.opirr(-p.As), uint32(v), uint32(REGTMP), uint32(p.To.Reg))

		case 4:
			o1 = OP_12IRR(c.opirr(-p.As), uint32(v), uint32(r), uint32(p.To.Reg))
		}

	case 29: // mov fr,[sl]ext/auto/oreg
		v := c.regoff(&p.To)
		r := int(p.To.Reg)
		if r == 0 {
			r = int(o.param)
		}
		switch o.size {
		case 12:
			o1 = OP_IR(c.opir(ALU12IW), uint32((v+1<<11)>>12), uint32(REGTMP))
			o2 = OP_RRR(c.oprrr(add), uint32(r), uint32(REGTMP), uint32(REGTMP))
			o3 = OP_12IRR(c.opirr(p.As), uint32(v), uint32(REGTMP), uint32(p.From.Reg))

		case 4:
			o1 = OP_12IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.From.Reg))
		}

	case 30: // mov gr/fr/fcc/fcsr, fr/fcc/fcsr/gr
		a := c.specialFpMovInst(p.As, oclass(&p.From), oclass(&p.To))
		o1 = OP_RR(a, uint32(p.From.Reg), uint32(p.To.Reg))

	case 31: // vsll $ui5, [vr1], vr2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_5IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 32: // vsll $ui6, [vr1], vr2
		v := c.regoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_6IRR(c.opirr(p.As), uint32(v), uint32(r), uint32(p.To.Reg))

	case 34: // mov $con,fr
		v := c.regoff(&p.From)
		a := AADDU
		if v > 0 {
			a = AOR
		}
		a2 := c.specialFpMovInst(p.As, C_REG, oclass(&p.To))
		o1 = OP_12IRR(c.opirr(a), uint32(v), uint32(0), uint32(REGTMP))
		o2 = OP_RR(a2, uint32(REGTMP), uint32(p.To.Reg))

	case 35: // mov r,lext/auto/oreg
		v := c.regoff(&p.To)
		r := int(p.To.Reg)
		if r == 0 {
			r = int(o.param)
		}
		o1 = OP_IR(c.opir(ALU12IW), uint32((v+1<<11)>>12), uint32(REGTMP))
		o2 = OP_RRR(c.oprrr(add), uint32(r), uint32(REGTMP), uint32(REGTMP))
		o3 = OP_12IRR(c.opirr(p.As), uint32(v), uint32(REGTMP), uint32(p.From.Reg))

	case 36: // mov lext/auto/oreg,r
		v := c.regoff(&p.From)
		r := int(p.From.Reg)
		if r == 0 {
			r = int(o.param)
		}
		o1 = OP_IR(c.opir(ALU12IW), uint32((v+1<<11)>>12), uint32(REGTMP))
		o2 = OP_RRR(c.oprrr(add), uint32(r), uint32(REGTMP), uint32(REGTMP))
		o3 = OP_12IRR(c.opirr(-p.As), uint32(v), uint32(REGTMP), uint32(p.To.Reg))

	case 37: // fmadd r1, r2, [r3], r4
		r := int(p.To.Reg)
		if len(p.RestArgs) > 0 {
			r = int(p.GetFrom3().Reg)
		}
		o1 = OP_RRRR(c.oprrrr(p.As), uint32(p.From.Reg), uint32(p.Reg), uint32(r), uint32(p.To.Reg))

	case 38: // word
		o1 = uint32(c.regoff(&p.From))

	case 39: // vmov Rn, Vd.<T>[index]
		v, m := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		Rj := uint32(p.From.Reg & EXT_REG_MASK)
		Vd := uint32(p.To.Reg & EXT_REG_MASK)
		index := uint32(p.To.Index)
		c.checkindex(p, index, m)
		o1 = v | (index << 10) | (Rj << 5) | Vd

	case 40: // vmov Vd.<T>[index], Rn
		v, m := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		Vj := uint32(p.From.Reg & EXT_REG_MASK)
		Rd := uint32(p.To.Reg & EXT_REG_MASK)
		index := uint32(p.From.Index)
		c.checkindex(p, index, m)
		o1 = v | (index << 10) | (Vj << 5) | Rd

	case 41: // vmov Rn, Vd.<T>
		v, _ := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		Rj := uint32(p.From.Reg & EXT_REG_MASK)
		Vd := uint32(p.To.Reg & EXT_REG_MASK)
		o1 = v | (Rj << 5) | Vd

	case 42: // vmov  xj, xd.<T>
		v, _ := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		Xj := uint32(p.From.Reg & EXT_REG_MASK)
		Xd := uint32(p.To.Reg & EXT_REG_MASK)
		o1 = v | (Xj << 5) | Xd

	case 43: // vmov  xj, xd.<T>[index]
		v, m := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		Xj := uint32(p.From.Reg & EXT_REG_MASK)
		Xd := uint32(p.To.Reg & EXT_REG_MASK)
		index := uint32(p.To.Index)
		c.checkindex(p, index, m)
		o1 = v | (index << 10) | (Xj << 5) | Xd

	case 44: // vmov  xj.<T>[index], xd
		v, m := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		Xj := uint32(p.From.Reg & EXT_REG_MASK)
		Xd := uint32(p.To.Reg & EXT_REG_MASK)
		index := uint32(p.From.Index)
		c.checkindex(p, index, m)
		o1 = v | (index << 10) | (Xj << 5) | Xd

	case 45: // vmov  vj.<T>[index], vd.<T>
		v, m := c.specialLsxMovInst(p.As, p.From.Reg, p.To.Reg)
		if v == 0 {
			c.ctxt.Diag("illegal arng type combination: %v\n", p)
		}

		vj := uint32(p.From.Reg & EXT_REG_MASK)
		vd := uint32(p.To.Reg & EXT_REG_MASK)
		index := uint32(p.From.Index)
		c.checkindex(p, index, m)
		o1 = v | (index << 10) | (vj << 5) | vd

	case 46:
		// preld  offset(Rbase), hint
		offs := c.regoff(&p.From)
		hint := p.GetFrom3().Offset
		o1 = OP_12IR_5I(c.opiir(p.As), uint32(offs), uint32(p.From.Reg), uint32(hint))

	case 47:
		// preldx (Rbase)(Roff), hint
		hint := p.GetFrom3().Offset
		o1 = OP_5IRR(c.opirr(p.As), uint32(p.From.Index), uint32(p.From.Reg), uint32(hint))

	case 49:
		if p.As == ANOOP {
			// andi r0, r0, 0
			o1 = OP_12IRR(c.opirr(AAND), 0, 0, 0)
		} else {
			// undef
			o1 = OP_15I(c.opi(ABREAK), 0)
		}

	// relocation operations
	case 50: // mov r,addr ==> pcalau12i + sw
		o1 = OP_IR(c.opir(APCALAU12I), uint32(0), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_ADDR_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.To.Sym,
			Add:  p.To.Offset,
		})
		o2 = OP_12IRR(c.opirr(p.As), uint32(0), uint32(REGTMP), uint32(p.From.Reg))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_ADDR_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.To.Sym,
			Add:  p.To.Offset,
		})

	case 51: // mov addr,r ==> pcalau12i + lw
		o1 = OP_IR(c.opir(APCALAU12I), uint32(0), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_ADDR_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.From.Sym,
			Add:  p.From.Offset,
		})
		o2 = OP_12IRR(c.opirr(-p.As), uint32(0), uint32(REGTMP), uint32(p.To.Reg))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_ADDR_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.From.Sym,
			Add:  p.From.Offset,
		})

	case 52: // mov $ext, r
		// NOTE: this case does not use REGTMP. If it ever does,
		// remove the NOTUSETMP flag in optab.
		o1 = OP_IR(c.opir(APCALAU12I), uint32(0), uint32(p.To.Reg))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_ADDR_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.From.Sym,
			Add:  p.From.Offset,
		})
		o2 = OP_12IRR(c.opirr(add), uint32(0), uint32(p.To.Reg), uint32(p.To.Reg))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_ADDR_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.From.Sym,
			Add:  p.From.Offset,
		})

	case 53: // mov r, tlsvar ==>  lu12i.w + ori + add r2, regtmp + sw o(regtmp)
		// NOTE: this case does not use REGTMP. If it ever does,
		// remove the NOTUSETMP flag in optab.
		o1 = OP_IR(c.opir(ALU12IW), uint32(0), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_LE_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.To.Sym,
			Add:  p.To.Offset,
		})
		o2 = OP_12IRR(c.opirr(AOR), uint32(0), uint32(REGTMP), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_LE_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.To.Sym,
			Add:  p.To.Offset,
		})
		o3 = OP_RRR(c.oprrr(AADDV), uint32(REG_R2), uint32(REGTMP), uint32(REGTMP))
		o4 = OP_12IRR(c.opirr(p.As), uint32(0), uint32(REGTMP), uint32(p.From.Reg))

	case 54: // lu12i.w + ori + add r2, regtmp + lw o(regtmp)
		// NOTE: this case does not use REGTMP. If it ever does,
		// remove the NOTUSETMP flag in optab.
		o1 = OP_IR(c.opir(ALU12IW), uint32(0), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_LE_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.From.Sym,
			Add:  p.From.Offset,
		})
		o2 = OP_12IRR(c.opirr(AOR), uint32(0), uint32(REGTMP), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_LE_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.From.Sym,
			Add:  p.From.Offset,
		})
		o3 = OP_RRR(c.oprrr(AADDV), uint32(REG_R2), uint32(REGTMP), uint32(REGTMP))
		o4 = OP_12IRR(c.opirr(-p.As), uint32(0), uint32(REGTMP), uint32(p.To.Reg))

	case 56: // mov r, tlsvar IE model ==> (pcalau12i + ld.d)tlsvar@got + add.d + st.d
		o1 = OP_IR(c.opir(APCALAU12I), uint32(0), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_IE_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.To.Sym,
		})
		o2 = OP_12IRR(c.opirr(-p.As), uint32(0), uint32(REGTMP), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_IE_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.To.Sym,
		})
		o3 = OP_RRR(c.oprrr(AADDVU), uint32(REGTMP), uint32(REG_R2), uint32(REGTMP))
		o4 = OP_12IRR(c.opirr(p.As), uint32(0), uint32(REGTMP), uint32(p.From.Reg))

	case 57: // mov tlsvar, r IE model ==> (pcalau12i + ld.d)tlsvar@got + add.d + ld.d
		o1 = OP_IR(c.opir(APCALAU12I), uint32(0), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_IE_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.From.Sym,
		})
		o2 = OP_12IRR(c.opirr(-p.As), uint32(0), uint32(REGTMP), uint32(REGTMP))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_TLS_IE_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.From.Sym,
		})
		o3 = OP_RRR(c.oprrr(AADDVU), uint32(REGTMP), uint32(REG_R2), uint32(REGTMP))
		o4 = OP_12IRR(c.opirr(-p.As), uint32(0), uint32(REGTMP), uint32(p.To.Reg))

	case 59: // mov $dcon,r
		// NOTE: this case does not use REGTMP. If it ever does,
		// remove the NOTUSETMP flag in optab.
		v := c.vregoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
		o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(p.To.Reg), uint32(p.To.Reg))
		o3 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
		o4 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))

	case 60: // add $dcon,r1,r2
		v := c.vregoff(&p.From)
		o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
		o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(REGTMP), uint32(REGTMP))
		o3 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
		o4 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o5 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 61: // word C_DCON
		o1 = uint32(c.vregoff(&p.From))
		o2 = uint32(c.vregoff(&p.From) >> 32)

	case 62: // rdtimex rd, rj
		o1 = OP_RR(c.oprr(p.As), uint32(p.To.Reg), uint32(p.RegTo2))

	case 65: // mov sym@GOT, r ==> pcalau12i + ld.d
		o1 = OP_IR(c.opir(APCALAU12I), uint32(0), uint32(p.To.Reg))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_GOT_HI,
			Off:  int32(c.pc),
			Siz:  4,
			Sym:  p.From.Sym,
		})
		o2 = OP_12IRR(c.opirr(-p.As), uint32(0), uint32(p.To.Reg), uint32(p.To.Reg))
		c.cursym.AddRel(c.ctxt, obj.Reloc{
			Type: objabi.R_LOONG64_GOT_LO,
			Off:  int32(c.pc + 4),
			Siz:  4,
			Sym:  p.From.Sym,
		})

	case 66: // am* From, To, RegTo2 ==> am* RegTo2, From, To
		rk := p.From.Reg
		rj := p.To.Reg
		rd := p.RegTo2

		// See section 2.2.7.1 of https://loongson.github.io/LoongArch-Documentation/LoongArch-Vol1-EN.html
		// for the register usage constraints.
		if rd == rj || rd == rk {
			c.ctxt.Diag("illegal register combination: %v\n", p)
		}
		o1 = OP_RRR(atomicInst[p.As], uint32(rk), uint32(rj), uint32(rd))

	case 67: // mov $dcon12_0, r
		v := c.vregoff(&p.From)
		o1 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(0), uint32(p.To.Reg))

	case 68: // mov $dcon12_20S, r
		v := c.vregoff(&p.From)
		contype := c.aclass(&p.From)
		switch contype {
		default: // C_DCON12_20S
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
			o2 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		case C_DCON20S_20:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
		case C_DCON12_12S:
			o1 = OP_12IRR(c.opirr(AADDV), uint32(v), uint32(0), uint32(p.To.Reg))
			o2 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		case C_DCON20S_12S, C_DCON20S_0:
			o1 = OP_12IRR(c.opirr(AADD), uint32(v), uint32(0), uint32(p.To.Reg))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
		case C_DCON12_12U:
			o1 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(0), uint32(p.To.Reg))
			o2 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		case C_DCON20S_12U:
			o1 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(0), uint32(p.To.Reg))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
		}

	case 69: // mov $dcon32_12S, r
		v := c.vregoff(&p.From)
		contype := c.aclass(&p.From)
		switch contype {
		default: // C_DCON32_12S, C_DCON32_0
			o1 = OP_12IRR(c.opirr(AADD), uint32(v), uint32(0), uint32(p.To.Reg))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		case C_DCON32_20:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		case C_DCON12_32S:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
			o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(p.To.Reg), uint32(p.To.Reg))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		case C_DCON20S_32:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(p.To.Reg))
			o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(p.To.Reg), uint32(p.To.Reg))
			o3 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
		case C_DCON32_12U:
			o1 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(0), uint32(p.To.Reg))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(p.To.Reg))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(p.To.Reg), uint32(p.To.Reg))
		}

	case 70: // add $dcon12_0,[r1],r2
		v := c.vregoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		o1 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(0), uint32(REGTMP))
		o2 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 71: // add $dcon12_20S,[r1],r2
		v := c.vregoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		contype := c.aclass(&p.From)
		switch contype {
		default: // C_DCON12_20S
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
			o2 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		case C_DCON20S_20:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
		case C_DCON12_12S:
			o1 = OP_12IRR(c.opirr(AADDV), uint32(v), uint32(0), uint32(REGTMP))
			o2 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		case C_DCON20S_12S, C_DCON20S_0:
			o1 = OP_12IRR(c.opirr(AADD), uint32(v), uint32(0), uint32(REGTMP))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
		case C_DCON12_12U:
			o1 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(0), uint32(REGTMP))
			o2 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		case C_DCON20S_12U:
			o1 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(0), uint32(REGTMP))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
		}
		o3 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))

	case 72: // add $dcon32_12S,[r1],r2
		v := c.vregoff(&p.From)
		r := int(p.Reg)
		if r == 0 {
			r = int(p.To.Reg)
		}
		contype := c.aclass(&p.From)
		switch contype {
		default: // C_DCON32_12S, C_DCON32_0
			o1 = OP_12IRR(c.opirr(AADD), uint32(v), uint32(0), uint32(REGTMP))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		case C_DCON32_20:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		case C_DCON12_32S:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
			o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(REGTMP), uint32(REGTMP))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		case C_DCON20S_32:
			o1 = OP_IR(c.opir(ALU12IW), uint32(v>>12), uint32(REGTMP))
			o2 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(REGTMP), uint32(REGTMP))
			o3 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
		case C_DCON32_12U:
			o1 = OP_12IRR(c.opirr(AOR), uint32(v), uint32(0), uint32(REGTMP))
			o2 = OP_IR(c.opir(ALU32ID), uint32(v>>32), uint32(REGTMP))
			o3 = OP_12IRR(c.opirr(ALU52ID), uint32(v>>52), uint32(REGTMP), uint32(REGTMP))
		}
		o4 = OP_RRR(c.oprrr(p.As), uint32(REGTMP), uint32(r), uint32(p.To.Reg))
	}

	out[0] = o1
	out[1] = o2
	out[2] = o3
	out[3] = o4
	out[4] = o5
}

// checkoperand checks if operand >= 0 && operand <= maxoperand
func (c *ctxt0) checkoperand(p *obj.Prog, operand uint32, mask uint32) {
	if (operand & ^mask) != 0 {
		c.ctxt.Diag("operand out of range 0 to %d: %v", mask, p)
	}
}

// checkindex checks if index >= 0 && index <= maxindex
func (c *ctxt0) checkindex(p *obj.Prog, index uint32, mask uint32) {
	if (index & ^mask) != 0 {
		c.ctxt.Diag("register element index out of range 0 to %d: %v", mask, p)
	}
}

func (c *ctxt0) vregoff(a *obj.Addr) int64 {
	c.instoffset = 0
	c.aclass(a)
	return c.instoffset
}

func (c *ctxt0) regoff(a *obj.Addr) int32 {
	return int32(c.vregoff(a))
}

func (c *ctxt0) oprrrr(a obj.As) uint32 {
	switch a {
	case AFMADDF:
		return 0x81 << 20 // fmadd.s
	case AFMADDD:
		return 0x82 << 20 // fmadd.d
	case AFMSUBF:
		return 0x85 << 20 // fmsub.s
	case AFMSUBD:
		return 0x86 << 20 // fmsub.d
	case AFNMADDF:
		return 0x89 << 20 // fnmadd.f
	case AFNMADDD:
		return 0x8a << 20 // fnmadd.d
	case AFNMSUBF:
		return 0x8d << 20 // fnmsub.s
	case AFNMSUBD:
		return 0x8e << 20 // fnmsub.d
	}

	c.ctxt.Diag("bad rrrr opcode %v", a)
	return 0
}

func (c *ctxt0) oprrr(a obj.As) uint32 {
	switch a {
	case AADD:
		return 0x20 << 15
	case AADDU:
		return 0x20 << 15
	case ASGT:
		return 0x24 << 15 // SLT
	case ASGTU:
		return 0x25 << 15 // SLTU
	case AMASKEQZ:
		return 0x26 << 15
	case AMASKNEZ:
		return 0x27 << 15
	case AAND:
		return 0x29 << 15
	case AOR:
		return 0x2a << 15
	case AXOR:
		return 0x2b << 15
	case AORN:
		return 0x2c << 15 // orn
	case AANDN:
		return 0x2d << 15 // andn
	case ASUB:
		return 0x22 << 15
	case ASUBU, ANEGW:
		return 0x22 << 15
	case ANOR:
		return 0x28 << 15
	case ASLL:
		return 0x2e << 15
	case ASRL:
		return 0x2f << 15
	case ASRA:
		return 0x30 << 15
	case AROTR:
		return 0x36 << 15
	case ASLLV:
		return 0x31 << 15
	case ASRLV:
		return 0x32 << 15
	case ASRAV:
		return 0x33 << 15
	case AROTRV:
		return 0x37 << 15
	case AADDV:
		return 0x21 << 15
	case AADDVU:
		return 0x21 << 15
	case ASUBV:
		return 0x23 << 15
	case ASUBVU, ANEGV:
		return 0x23 << 15

	case AMUL:
		return 0x38 << 15 // mul.w
	case AMULU:
		return 0x38 << 15 // mul.w
	case AMULH:
		return 0x39 << 15 // mulh.w
	case AMULHU:
		return 0x3a << 15 // mulhu.w
	case AMULV:
		return 0x3b << 15 // mul.d
	case AMULVU:
		return 0x3b << 15 // mul.d
	case AMULHV:
		return 0x3c << 15 // mulh.d
	case AMULHVU:
		return 0x3d << 15 // mulhu.d
	case ADIV:
		return 0x40 << 15 // div.w
	case ADIVU:
		return 0x42 << 15 // div.wu
	case ADIVV:
		return 0x44 << 15 // div.d
	case ADIVVU:
		return 0x46 << 15 // div.du
	case AREM:
		return 0x41 << 15 // mod.w
	case AREMU:
		return 0x43 << 15 // mod.wu
	case AREMV:
		return 0x45 << 15 // mod.d
	case AREMVU:
		return 0x47 << 15 // mod.du
	case ACRCWBW:
		return 0x48 << 15 // crc.w.b.w
	case ACRCWHW:
		return 0x49 << 15 // crc.w.h.w
	case ACRCWWW:
		return 0x4a << 15 // crc.w.w.w
	case ACRCWVW:
		return 0x4b << 15 // crc.w.d.w
	case ACRCCWBW:
		return 0x4c << 15 // crcc.w.b.w
	case ACRCCWHW:
		return 0x4d << 15 // crcc.w.h.w
	case ACRCCWWW:
		return 0x4e << 15 // crcc.w.w.w
	case ACRCCWVW:
		return 0x4f << 15 // crcc.w.d.w
	case AJMP:
		return 0x13 << 26 // jirl r0, rj, 0
	case AJAL:
		return (0x13 << 26) | 1 // jirl r1, rj, 0

	case ADIVF:
		return 0x20d << 15
	case ADIVD:
		return 0x20e << 15
	case AMULF:
		return 0x209 << 15
	case AMULD:
		return 0x20a << 15
	case ASUBF:
		return 0x205 << 15
	case ASUBD:
		return 0x206 << 15
	case AADDF:
		return 0x201 << 15
	case AADDD:
		return 0x202 << 15
	case ACMPEQF:
		return 0x0c1<<20 | 0x4<<15 // FCMP.CEQ.S
	case ACMPEQD:
		return 0x0c2<<20 | 0x4<<15 // FCMP.CEQ.D
	case ACMPGED:
		return 0x0c2<<20 | 0x7<<15 // FCMP.SLE.D
	case ACMPGEF:
		return 0x0c1<<20 | 0x7<<15 // FCMP.SLE.S
	case ACMPGTD:
		return 0x0c2<<20 | 0x3<<15 // FCMP.SLT.D
	case ACMPGTF:
		return 0x0c1<<20 | 0x3<<15 // FCMP.SLT.S
	case AFMINF:
		return 0x215 << 15 // fmin.s
	case AFMIND:
		return 0x216 << 15 // fmin.d
	case AFMAXF:
		return 0x211 << 15 // fmax.s
	case AFMAXD:
		return 0x212 << 15 // fmax.d
	case AFMAXAF:
		return 0x219 << 15 // fmaxa.s
	case AFMAXAD:
		return 0x21a << 15 // fmaxa.d
	case AFMINAF:
		return 0x21d << 15 // fmina.s
	case AFMINAD:
		return 0x21e << 15 // fmina.d
	case AFSCALEBF:
		return 0x221 << 15 // fscaleb.s
	case AFSCALEBD:
		return 0x222 << 15 // fscaleb.d
	case AFCOPYSGF:
		return 0x225 << 15 // fcopysign.s
	case AFCOPYSGD:
		return 0x226 << 15 // fcopysign.d
	case -AMOVB:
		return 0x07000 << 15 // ldx.b
	case -AMOVH:
		return 0x07008 << 15 // ldx.h
	case -AMOVW:
		return 0x07010 << 15 // ldx.w
	case -AMOVV:
		return 0x07018 << 15 // ldx.d
	case -AMOVBU:
		return 0x07040 << 15 // ldx.bu
	case -AMOVHU:
		return 0x07048 << 15 // ldx.hu
	case -AMOVWU:
		return 0x07050 << 15 // ldx.wu
	case AMOVB:
		return 0x07020 << 15 // stx.b
	case AMOVH:
		return 0x07028 << 15 // stx.h
	case AMOVW:
		return 0x07030 << 15 // stx.w
	case AMOVV:
		return 0x07038 << 15 // stx.d
	case -AMOVF:
		return 0x07060 << 15 // fldx.s
	case -AMOVD:
		return 0x07068 << 15 // fldx.d
	case AMOVF:
		return 0x07070 << 15 // fstx.s
	case AMOVD:
		return 0x07078 << 15 // fstx.d
	case -AVMOVQ:
		return 0x07080 << 15 // vldx
	case -AXVMOVQ:
		return 0x07090 << 15 // xvldx
	case AVMOVQ:
		return 0x07088 << 15 // vstx
	case AXVMOVQ:
		return 0x07098 << 15 // xvstx
	case AVSEQB:
		return 0x0e000 << 15 // vseq.b
	case AXVSEQB:
		return 0x0e800 << 15 // xvseq.b
	case AVSEQH:
		return 0x0e001 << 15 // vseq.h
	case AXVSEQH:
		return 0x0e801 << 15 // xvseq.h
	case AVSEQW:
		return 0x0e002 << 15 // vseq.w
	case AXVSEQW:
		return 0x0e802 << 15 // xvseq.w
	case AVSEQV:
		return 0x0e003 << 15 // vseq.d
	case AXVSEQV:
		return 0x0e803 << 15 // xvseq.d
	case AVANDV:
		return 0x0E24C << 15 // vand.v
	case AVORV:
		return 0x0E24D << 15 // vor.v
	case AVXORV:
		return 0x0E24E << 15 // vxor.v
	case AVNORV:
		return 0x0E24F << 15 // vnor.v
	case AVANDNV:
		return 0x0E250 << 15 // vandn.v
	case AVORNV:
		return 0x0E251 << 15 // vorn.v
	case AXVANDV:
		return 0x0EA4C << 15 // xvand.v
	case AXVORV:
		return 0x0EA4D << 15 // xvor.v
	case AXVXORV:
		return 0x0EA4E << 15 // xvxor.v
	case AXVNORV:
		return 0x0EA4F << 15 // xvnor.v
	case AXVANDNV:
		return 0x0EA50 << 15 // xvandn.v
	case AXVORNV:
		return 0x0EA51 << 15 // xvorn.v
	case AVDIVB:
		return 0xe1c0 << 15 // vdiv.b
	case AVDIVH:
		return 0xe1c1 << 15 // vdiv.h
	case AVDIVW:
		return 0xe1c2 << 15 // vdiv.w
	case AVDIVV:
		return 0xe1c3 << 15 // vdiv.d
	case AVMODB:
		return 0xe1c4 << 15 // vmod.b
	case AVMODH:
		return 0xe1c5 << 15 // vmod.h
	case AVMODW:
		return 0xe1c6 << 15 // vmod.w
	case AVMODV:
		return 0xe1c7 << 15 // vmod.d
	case AVDIVBU:
		return 0xe1c8 << 15 // vdiv.bu
	case AVDIVHU:
		return 0xe1c9 << 15 // vdiv.hu
	case AVDIVWU:
		return 0xe1ca << 15 // vdiv.wu
	case AVDIVVU:
		return 0xe1cb << 15 // vdiv.du
	case AVMODBU:
		return 0xe1cc << 15 // vmod.bu
	case AVMODHU:
		return 0xe1cd << 15 // vmod.hu
	case AVMODWU:
		return 0xe1ce << 15 // vmod.wu
	case AVMODVU:
		return 0xe1cf << 15 // vmod.du
	case AXVDIVB:
		return 0xe9c0 << 15 // xvdiv.b
	case AXVDIVH:
		return 0xe9c1 << 15 // xvdiv.h
	case AXVDIVW:
		return 0xe9c2 << 15 // xvdiv.w
	case AXVDIVV:
		return 0xe9c3 << 15 // xvdiv.d
	case AXVMODB:
		return 0xe9c4 << 15 // xvmod.b
	case AXVMODH:
		return 0xe9c5 << 15 // xvmod.h
	case AXVMODW:
		return 0xe9c6 << 15 // xvmod.w
	case AXVMODV:
		return 0xe9c7 << 15 // xvmod.d
	case AXVDIVBU:
		return 0xe9c8 << 15 // xvdiv.bu
	case AXVDIVHU:
		return 0xe9c9 << 15 // xvdiv.hu
	case AXVDIVWU:
		return 0xe9ca << 15 // xvdiv.wu
	case AXVDIVVU:
		return 0xe9cb << 15 // xvdiv.du
	case AXVMODBU:
		return 0xe9cc << 15 // xvmod.bu
	case AXVMODHU:
		return 0xe9cd << 15 // xvmod.hu
	case AXVMODWU:
		return 0xe9ce << 15 // xvmod.wu
	case AXVMODVU:
		return 0xe9cf << 15 // xvmod.du
	case AVMULWEVHB:
		return 0xe120 << 15 // vmulwev.h.b
	case AVMULWEVWH:
		return 0xe121 << 15 // vmulwev.w.h
	case AVMULWEVVW:
		return 0xe122 << 15 // vmulwev.d.w
	case AVMULWEVQV:
		return 0xe123 << 15 // vmulwev.q.d
	case AVMULWODHB:
		return 0xe124 << 15 // vmulwod.h.b
	case AVMULWODWH:
		return 0xe125 << 15 // vmulwod.w.h
	case AVMULWODVW:
		return 0xe126 << 15 // vmulwod.d.w
	case AVMULWODQV:
		return 0xe127 << 15 // vmulwod.q.d
	case AVMULWEVHBU:
		return 0xe130 << 15 // vmulwev.h.bu
	case AVMULWEVWHU:
		return 0xe131 << 15 // vmulwev.w.hu
	case AVMULWEVVWU:
		return 0xe132 << 15 // vmulwev.d.wu
	case AVMULWEVQVU:
		return 0xe133 << 15 // vmulwev.q.du
	case AVMULWODHBU:
		return 0xe134 << 15 // vmulwod.h.bu
	case AVMULWODWHU:
		return 0xe135 << 15 // vmulwod.w.hu
	case AVMULWODVWU:
		return 0xe136 << 15 // vmulwod.d.wu
	case AVMULWODQVU:
		return 0xe137 << 15 // vmulwod.q.du
	case AVMULWEVHBUB:
		return 0xe140 << 15 // vmulwev.h.bu.b
	case AVMULWEVWHUH:
		return 0xe141 << 15 // vmulwev.w.hu.h
	case AVMULWEVVWUW:
		return 0xe142 << 15 // vmulwev.d.wu.w
	case AVMULWEVQVUV:
		return 0xe143 << 15 // vmulwev.q.du.d
	case AVMULWODHBUB:
		return 0xe144 << 15 // vmulwod.h.bu.b
	case AVMULWODWHUH:
		return 0xe145 << 15 // vmulwod.w.hu.h
	case AVMULWODVWUW:
		return 0xe146 << 15 // vmulwod.d.wu.w
	case AVMULWODQVUV:
		return 0xe147 << 15 // vmulwod.q.du.d
	case AXVMULWEVHB:
		return 0xe920 << 15 // xvmulwev.h.b
	case AXVMULWEVWH:
		return 0xe921 << 15 // xvmulwev.w.h
	case AXVMULWEVVW:
		return 0xe922 << 15 // xvmulwev.d.w
	case AXVMULWEVQV:
		return 0xe923 << 15 // xvmulwev.q.d
	case AXVMULWODHB:
		return 0xe924 << 15 // xvmulwod.h.b
	case AXVMULWODWH:
		return 0xe925 << 15 // xvmulwod.w.h
	case AXVMULWODVW:
		return 0xe926 << 15 // xvmulwod.d.w
	case AXVMULWODQV:
		return 0xe927 << 15 // xvmulwod.q.d
	case AXVMULWEVHBU:
		return 0xe930 << 15 // xvmulwev.h.bu
	case AXVMULWEVWHU:
		return 0xe931 << 15 // xvmulwev.w.hu
	case AXVMULWEVVWU:
		return 0xe932 << 15 // xvmulwev.d.wu
	case AXVMULWEVQVU:
		return 0xe933 << 15 // xvmulwev.q.du
	case AXVMULWODHBU:
		return 0xe934 << 15 // xvmulwod.h.bu
	case AXVMULWODWHU:
		return 0xe935 << 15 // xvmulwod.w.hu
	case AXVMULWODVWU:
		return 0xe936 << 15 // xvmulwod.d.wu
	case AXVMULWODQVU:
		return 0xe937 << 15 // xvmulwod.q.du
	case AXVMULWEVHBUB:
		return 0xe940 << 15 // xvmulwev.h.bu.b
	case AXVMULWEVWHUH:
		return 0xe941 << 15 // xvmulwev.w.hu.h
	case AXVMULWEVVWUW:
		return 0xe942 << 15 // xvmulwev.d.wu.w
	case AXVMULWEVQVUV:
		return 0xe943 << 15 // xvmulwev.q.du.d
	case AXVMULWODHBUB:
		return 0xe944 << 15 // xvmulwod.h.bu.b
	case AXVMULWODWHUH:
		return 0xe945 << 15 // xvmulwod.w.hu.h
	case AXVMULWODVWUW:
		return 0xe946 << 15 // xvmulwod.d.wu.w
	case AXVMULWODQVUV:
		return 0xe947 << 15 // xvmulwod.q.du.d
	case AVSLLB:
		return 0xe1d0 << 15 // vsll.b
	case AVSLLH:
		return 0xe1d1 << 15 // vsll.h
	case AVSLLW:
		return 0xe1d2 << 15 // vsll.w
	case AVSLLV:
		return 0xe1d3 << 15 // vsll.d
	case AVSRLB:
		return 0xe1d4 << 15 // vsrl.b
	case AVSRLH:
		return 0xe1d5 << 15 // vsrl.h
	case AVSRLW:
		return 0xe1d6 << 15 // vsrl.w
	case AVSRLV:
		return 0xe1d7 << 15 // vsrl.d
	case AVSRAB:
		return 0xe1d8 << 15 // vsra.b
	case AVSRAH:
		return 0xe1d9 << 15 // vsra.h
	case AVSRAW:
		return 0xe1da << 15 // vsra.w
	case AVSRAV:
		return 0xe1db << 15 // vsra.d
	case AVROTRB:
		return 0xe1dc << 15 // vrotr.b
	case AVROTRH:
		return 0xe1dd << 15 // vrotr.h
	case AVROTRW:
		return 0xe1de << 15 // vrotr.w
	case AVROTRV:
		return 0xe1df << 15 // vrotr.d
	case AXVSLLB:
		return 0xe9d0 << 15 // xvsll.b
	case AXVSLLH:
		return 0xe9d1 << 15 // xvsll.h
	case AXVSLLW:
		return 0xe9d2 << 15 // xvsll.w
	case AXVSLLV:
		return 0xe9d3 << 15 // xvsll.d
	case AXVSRLB:
		return 0xe9d4 << 15 // xvsrl.b
	case AXVSRLH:
		return 0xe9d5 << 15 // xvsrl.h
	case AXVSRLW:
		return 0xe9d6 << 15 // xvsrl.w
	case AXVSRLV:
		return 0xe9d7 << 15 // xvsrl.d
	case AXVSRAB:
		return 0xe9d8 << 15 // xvsra.b
	case AXVSRAH:
		return 0xe9d9 << 15 // xvsra.h
	case AXVSRAW:
		return 0xe9da << 15 // xvsra.w
	case AXVSRAV:
		return 0xe9db << 15 // xvsra.d
	case AXVROTRB:
		return 0xe9dc << 15 // xvrotr.b
	case AXVROTRH:
		return 0xe9dd << 15 // xvrotr.h
	case AXVROTRW:
		return 0xe9de << 15 // xvrotr.w
	case AXVROTRV:
		return 0xe9df << 15 // xvrotr.d
	case AVADDB:
		return 0xe014 << 15 // vadd.b
	case AVADDH:
		return 0xe015 << 15 // vadd.h
	case AVADDW:
		return 0xe016 << 15 // vadd.w
	case AVADDV:
		return 0xe017 << 15 // vadd.d
	case AVADDQ:
		return 0xe25a << 15 // vadd.q
	case AVSUBB:
		return 0xe018 << 15 // vsub.b
	case AVSUBH:
		return 0xe019 << 15 // vsub.h
	case AVSUBW:
		return 0xe01a << 15 // vsub.w
	case AVSUBV:
		return 0xe01b << 15 // vsub.d
	case AVSUBQ:
		return 0xe25b << 15 // vsub.q
	case AXVADDB:
		return 0xe814 << 15 // xvadd.b
	case AXVADDH:
		return 0xe815 << 15 // xvadd.h
	case AXVADDW:
		return 0xe816 << 15 // xvadd.w
	case AXVADDV:
		return 0xe817 << 15 // xvadd.d
	case AXVADDQ:
		return 0xea5a << 15 // xvadd.q
	case AXVSUBB:
		return 0xe818 << 15 // xvsub.b
	case AXVSUBH:
		return 0xe819 << 15 // xvsub.h
	case AXVSUBW:
		return 0xe81a << 15 // xvsub.w
	case AXVSUBV:
		return 0xe81b << 15 // xvsub.d
	case AXVSUBQ:
		return 0xea5b << 15 // xvsub.q
	case AVILVLB:
		return 0xe234 << 15 // vilvl.b
	case AVILVLH:
		return 0xe235 << 15 // vilvl.h
	case AVILVLW:
		return 0xe236 << 15 // vilvl.w
	case AVILVLV:
		return 0xe237 << 15 // vilvl.d
	case AVILVHB:
		return 0xe238 << 15 // vilvh.b
	case AVILVHH:
		return 0xe239 << 15 // vilvh.h
	case AVILVHW:
		return 0xe23a << 15 // vilvh.w
	case AVILVHV:
		return 0xe23b << 15 // vilvh.d
	case AXVILVLB:
		return 0xea34 << 15 // xvilvl.b
	case AXVILVLH:
		return 0xea35 << 15 // xvilvl.h
	case AXVILVLW:
		return 0xea36 << 15 // xvilvl.w
	case AXVILVLV:
		return 0xea37 << 15 // xvilvl.d
	case AXVILVHB:
		return 0xea38 << 15 // xvilvh.b
	case AXVILVHH:
		return 0xea39 << 15 // xvilvh.h
	case AXVILVHW:
		return 0xea3a << 15 // xvilvh.w
	case AXVILVHV:
		return 0xea3b << 15 // xvilvh.d
	case AVMULB:
		return 0xe108 << 15 // vmul.b
	case AVMULH:
		return 0xe109 << 15 // vmul.h
	case AVMULW:
		return 0xe10a << 15 // vmul.w
	case AVMULV:
		return 0xe10b << 15 // vmul.d
	case AVMUHB:
		return 0xe10c << 15 // vmuh.b
	case AVMUHH:
		return 0xe10d << 15 // vmuh.h
	case AVMUHW:
		return 0xe10e << 15 // vmuh.w
	case AVMUHV:
		return 0xe10f << 15 // vmuh.d
	case AVMUHBU:
		return 0xe110 << 15 // vmuh.bu
	case AVMUHHU:
		return 0xe111 << 15 // vmuh.hu
	case AVMUHWU:
		return 0xe112 << 15 // vmuh.wu
	case AVMUHVU:
		return 0xe113 << 15 // vmuh.du
	case AXVMULB:
		return 0xe908 << 15 // xvmul.b
	case AXVMULH:
		return 0xe909 << 15 // xvmul.h
	case AXVMULW:
		return 0xe90a << 15 // xvmul.w
	case AXVMULV:
		return 0xe90b << 15 // xvmul.d
	case AXVMUHB:
		return 0xe90c << 15 // xvmuh.b
	case AXVMUHH:
		return 0xe90d << 15 // xvmuh.h
	case AXVMUHW:
		return 0xe90e << 15 // xvmuh.w
	case AXVMUHV:
		return 0xe90f << 15 // xvmuh.d
	case AXVMUHBU:
		return 0xe910 << 15 // xvmuh.bu
	case AXVMUHHU:
		return 0xe911 << 15 // xvmuh.hu
	case AXVMUHWU:
		return 0xe912 << 15 // xvmuh.wu
	case AXVMUHVU:
		return 0xe913 << 15 // xvmuh.du
	case AVADDF:
		return 0xe261 << 15 // vfadd.s
	case AVADDD:
		return 0xe262 << 15 // vfadd.d
	case AVSUBF:
		return 0xe265 << 15 // vfsub.s
	case AVSUBD:
		return 0xe266 << 15 // vfsub.d
	case AVMULF:
		return 0xe271 << 15 // vfmul.s
	case AVMULD:
		return 0xe272 << 15 // vfmul.d
	case AVDIVF:
		return 0xe275 << 15 // vfdiv.s
	case AVDIVD:
		return 0xe276 << 15 // vfdiv.d
	case AXVADDF:
		return 0xea61 << 15 // xvfadd.s
	case AXVADDD:
		return 0xea62 << 15 // xvfadd.d
	case AXVSUBF:
		return 0xea65 << 15 // xvfsub.s
	case AXVSUBD:
		return 0xea66 << 15 // xvfsub.d
	case AXVMULF:
		return 0xea71 << 15 // xvfmul.s
	case AXVMULD:
		return 0xea72 << 15 // xvfmul.d
	case AXVDIVF:
		return 0xea75 << 15 // xvfdiv.s
	case AXVDIVD:
		return 0xea76 << 15 // xvfdiv.d
	}

	if a < 0 {
		c.ctxt.Diag("bad rrr opcode -%v", -a)
	} else {
		c.ctxt.Diag("bad rrr opcode %v", a)
	}
	return 0
}

func (c *ctxt0) oprr(a obj.As) uint32 {
	switch a {
	case ACLOW:
		return 0x4 << 10 // clo.w
	case ACLZW:
		return 0x5 << 10 // clz.w
	case ACTOW:
		return 0x6 << 10 // cto.w
	case ACTZW:
		return 0x7 << 10 // ctz.w
	case ACLOV:
		return 0x8 << 10 // clo.d
	case ACLZV:
		return 0x9 << 10 // clz.d
	case ACTOV:
		return 0xa << 10 // cto.d
	case ACTZV:
		return 0xb << 10 // ctz.d
	case AREVB2H:
		return 0xc << 10 // revb.2h
	case AREVB4H:
		return 0xd << 10 // revb.4h
	case AREVB2W:
		return 0xe << 10 // revb.2w
	case AREVBV:
		return 0xf << 10 // revb.d
	case AREVH2W:
		return 0x10 << 10 // revh.2w
	case AREVHV:
		return 0x11 << 10 // revh.d
	case ABITREV4B:
		return 0x12 << 10 // bitrev.4b
	case ABITREV8B:
		return 0x13 << 10 // bitrev.8b
	case ABITREVW:
		return 0x14 << 10 // bitrev.w
	case ABITREVV:
		return 0x15 << 10 // bitrev.d
	case AEXTWH:
		return 0x16 << 10 // ext.w.h
	case AEXTWB:
		return 0x17 << 10 // ext.w.h
	case ACPUCFG:
		return 0x1b << 10
	case ARDTIMELW:
		return 0x18 << 10
	case ARDTIMEHW:
		return 0x19 << 10
	case ARDTIMED:
		return 0x1a << 10
	case ATRUNCFV:
		return 0x46a9 << 10
	case ATRUNCDV:
		return 0x46aa << 10
	case ATRUNCFW:
		return 0x46a1 << 10
	case ATRUNCDW:
		return 0x46a2 << 10
	case AMOVFV:
		return 0x46c9 << 10
	case AMOVDV:
		return 0x46ca << 10
	case AMOVVF:
		return 0x4746 << 10
	case AMOVVD:
		return 0x474a << 10
	case AMOVFW:
		return 0x46c1 << 10
	case AMOVDW:
		return 0x46c2 << 10
	case AMOVWF:
		return 0x4744 << 10
	case AMOVDF:
		return 0x4646 << 10
	case AMOVWD:
		return 0x4748 << 10
	case AMOVFD:
		return 0x4649 << 10
	case AABSF:
		return 0x4501 << 10
	case AABSD:
		return 0x4502 << 10
	case AMOVF:
		return 0x4525 << 10
	case AMOVD:
		return 0x4526 << 10
	case ANEGF:
		return 0x4505 << 10
	case ANEGD:
		return 0x4506 << 10
	case ASQRTF:
		return 0x4511 << 10
	case ASQRTD:
		return 0x4512 << 10
	case AFLOGBF:
		return 0x4509 << 10 // flogb.s
	case AFLOGBD:
		return 0x450a << 10 // flogb.d
	case AFCLASSF:
		return 0x450d << 10 // fclass.s
	case AFCLASSD:
		return 0x450e << 10 // fclass.d
	case AFFINTFW:
		return 0x4744 << 10 // ffint.s.w
	case AFFINTFV:
		return 0x4746 << 10 // ffint.s.l
	case AFFINTDW:
		return 0x4748 << 10 // ffint.d.w
	case AFFINTDV:
		return 0x474a << 10 // ffint.d.l
	case AFTINTWF:
		return 0x46c1 << 10 // ftint.w.s
	case AFTINTWD:
		return 0x46c2 << 10 // ftint.w.d
	case AFTINTVF:
		return 0x46c9 << 10 // ftint.l.s
	case AFTINTVD:
		return 0x46ca << 10 // ftint.l.d
	case AFTINTRMWF:
		return 0x4681 << 10 // ftintrm.w.s
	case AFTINTRMWD:
		return 0x4682 << 10 // ftintrm.w.d
	case AFTINTRMVF:
		return 0x4689 << 10 // ftintrm.l.s
	case AFTINTRMVD:
		return 0x468a << 10 // ftintrm.l.d
	case AFTINTRPWF:
		return 0x4691 << 10 // ftintrp.w.s
	case AFTINTRPWD:
		return 0x4692 << 10 // ftintrp.w.d
	case AFTINTRPVF:
		return 0x4699 << 10 // ftintrp.l.s
	case AFTINTRPVD:
		return 0x469a << 10 // ftintrp.l.d
	case AFTINTRZWF:
		return 0x46a1 << 10 // ftintrz.w.s
	case AFTINTRZWD:
		return 0x46a2 << 10 // ftintrz.w.d
	case AFTINTRZVF:
		return 0x46a9 << 10 // ftintrz.l.s
	case AFTINTRZVD:
		return 0x46aa << 10 // ftintrz.l.d
	case AFTINTRNEWF:
		return 0x46b1 << 10 // ftintrne.w.s
	case AFTINTRNEWD:
		return 0x46b2 << 10 // ftintrne.w.d
	case AFTINTRNEVF:
		return 0x46b9 << 10 // ftintrne.l.s
	case AFTINTRNEVD:
		return 0x46ba << 10 // ftintrne.l.d
	case AVPCNTB:
		return 0x1ca708 << 10 // vpcnt.b
	case AVPCNTH:
		return 0x1ca709 << 10 // vpcnt.h
	case AVPCNTW:
		return 0x1ca70a << 10 // vpcnt.w
	case AVPCNTV:
		return 0x1ca70b << 10 // vpcnt.v
	case AXVPCNTB:
		return 0x1da708 << 10 // xvpcnt.b
	case AXVPCNTH:
		return 0x1da709 << 10 // xvpcnt.h
	case AXVPCNTW:
		return 0x1da70a << 10 // xvpcnt.w
	case AXVPCNTV:
		return 0x1da70b << 10 // xvpcnt.v
	case AVFSQRTF:
		return 0x1ca739 << 10 // vfsqrt.s
	case AVFSQRTD:
		return 0x1ca73a << 10 // vfsqrt.d
	case AVFRECIPF:
		return 0x1ca73d << 10 // vfrecip.s
	case AVFRECIPD:
		return 0x1ca73e << 10 // vfrecip.d
	case AVFRSQRTF:
		return 0x1ca741 << 10 // vfrsqrt.s
	case AVFRSQRTD:
		return 0x1ca742 << 10 // vfrsqrt.d
	case AXVFSQRTF:
		return 0x1da739 << 10 // xvfsqrt.s
	case AXVFSQRTD:
		return 0x1da73a << 10 // xvfsqrt.d
	case AXVFRECIPF:
		return 0x1da73d << 10 // xvfrecip.s
	case AXVFRECIPD:
		return 0x1da73e << 10 // xvfrecip.d
	case AXVFRSQRTF:
		return 0x1da741 << 10 // xvfrsqrt.s
	case AXVFRSQRTD:
		return 0x1da742 << 10 // xvfrsqrt.d
	case AVNEGB:
		return 0x1ca70c << 10 // vneg.b
	case AVNEGH:
		return 0x1ca70d << 10 // vneg.h
	case AVNEGW:
		return 0x1ca70e << 10 // vneg.w
	case AVNEGV:
		return 0x1ca70f << 10 // vneg.d
	case AXVNEGB:
		return 0x1da70c << 10 // xvneg.b
	case AXVNEGH:
		return 0x1da70d << 10 // xvneg.h
	case AXVNEGW:
		return 0x1da70e << 10 // xvneg.w
	case AXVNEGV:
		return 0x1da70f << 10 // xvneg.d
	case AVFRINTRNEF:
		return 0x1ca75d << 10 // vfrintrne.s
	case AVFRINTRNED:
		return 0x1ca75e << 10 // vfrintrne.d
	case AVFRINTRZF:
		return 0x1ca759 << 10 // vfrintrz.s
	case AVFRINTRZD:
		return 0x1ca75a << 10 // vfrintrz.d
	case AVFRINTRPF:
		return 0x1ca755 << 10 // vfrintrp.s
	case AVFRINTRPD:
		return 0x1ca756 << 10 // vfrintrp.d
	case AVFRINTRMF:
		return 0x1ca751 << 10 // vfrintrm.s
	case AVFRINTRMD:
		return 0x1ca752 << 10 // vfrintrm.d
	case AVFRINTF:
		return 0x1ca74d << 10 // vfrint.s
	case AVFRINTD:
		return 0x1ca74e << 10 // vfrint.d
	case AXVFRINTRNEF:
		return 0x1da75d << 10 // xvfrintrne.s
	case AXVFRINTRNED:
		return 0x1da75e << 10 // xvfrintrne.d
	case AXVFRINTRZF:
		return 0x1da759 << 10 // xvfrintrz.s
	case AXVFRINTRZD:
		return 0x1da75a << 10 // xvfrintrz.d
	case AXVFRINTRPF:
		return 0x1da755 << 10 // xvfrintrp.s
	case AXVFRINTRPD:
		return 0x1da756 << 10 // xvfrintrp.d
	case AXVFRINTRMF:
		return 0x1da751 << 10 // xvfrintrm.s
	case AXVFRINTRMD:
		return 0x1da752 << 10 // xvfrintrm.d
	case AXVFRINTF:
		return 0x1da74d << 10 // xvfrint.s
	case AXVFRINTD:
		return 0x1da74e << 10 // xvfrint.d
	case AVFCLASSF:
		return 0x1ca735 << 10 // vfclass.s
	case AVFCLASSD:
		return 0x1ca736 << 10 // vfclass.d
	case AXVFCLASSF:
		return 0x1da735 << 10 // xvfclass.s
	case AXVFCLASSD:
		return 0x1da736 << 10 // xvfclass.d
	case AVSETEQV:
		return 0x1ca726<<10 | 0x0<<3 // vseteqz.v
	case AVSETNEV:
		return 0x1ca727<<10 | 0x0<<3 // vsetnez.v
	case AVSETANYEQB:
		return 0x1ca728<<10 | 0x0<<3 // vsetanyeqz.b
	case AVSETANYEQH:
		return 0x1ca729<<10 | 0x0<<3 // vsetanyeqz.h
	case AVSETANYEQW:
		return 0x1ca72a<<10 | 0x0<<3 // vsetanyeqz.w
	case AVSETANYEQV:
		return 0x1ca72b<<10 | 0x0<<3 // vsetanyeqz.d
	case AVSETALLNEB:
		return 0x1ca72c<<10 | 0x0<<3 // vsetallnez.b
	case AVSETALLNEH:
		return 0x1ca72d<<10 | 0x0<<3 // vsetallnez.h
	case AVSETALLNEW:
		return 0x1ca72e<<10 | 0x0<<3 // vsetallnez.w
	case AVSETALLNEV:
		return 0x1ca72f<<10 | 0x0<<3 // vsetallnez.d
	case AXVSETEQV:
		return 0x1da726<<10 | 0x0<<3 // xvseteqz.v
	case AXVSETNEV:
		return 0x1da727<<10 | 0x0<<3 // xvsetnez.v
	case AXVSETANYEQB:
		return 0x1da728<<10 | 0x0<<3 // xvsetanyeqz.b
	case AXVSETANYEQH:
		return 0x1da729<<10 | 0x0<<3 // xvsetanyeqz.h
	case AXVSETANYEQW:
		return 0x1da72a<<10 | 0x0<<3 // xvsetanyeqz.w
	case AXVSETANYEQV:
		return 0x1da72b<<10 | 0x0<<3 // xvsetanyeqz.d
	case AXVSETALLNEB:
		return 0x1da72c<<10 | 0x0<<3 // xvsetallnez.b
	case AXVSETALLNEH:
		return 0x1da72d<<10 | 0x0<<3 // xvsetallnez.h
	case AXVSETALLNEW:
		return 0x1da72e<<10 | 0x0<<3 // xvsetallnez.w
	case AXVSETALLNEV:
		return 0x1da72f<<10 | 0x0<<3 // xvsetallnez.d
	}

	c.ctxt.Diag("bad rr opcode %v", a)
	return 0
}

func (c *ctxt0) opi(a obj.As) uint32 {
	switch a {
	case ASYSCALL:
		return 0x56 << 15
	case ABREAK:
		return 0x54 << 15
	case ADBAR:
		return 0x70e4 << 15
	}

	c.ctxt.Diag("bad ic opcode %v", a)

	return 0
}

func (c *ctxt0) opir(a obj.As) uint32 {
	switch a {
	case ALU12IW:
		return 0x0a << 25
	case ALU32ID:
		return 0x0b << 25
	case APCALAU12I:
		return 0x0d << 25
	case APCADDU12I:
		return 0x0e << 25
	}
	return 0
}

func (c *ctxt0) opirr(a obj.As) uint32 {
	switch a {
	case AADD, AADDU:
		return 0x00a << 22
	case ASGT:
		return 0x008 << 22
	case ASGTU:
		return 0x009 << 22
	case AAND:
		return 0x00d << 22
	case AOR:
		return 0x00e << 22
	case ALU52ID:
		return 0x00c << 22
	case AXOR:
		return 0x00f << 22
	case ASLL:
		return 0x00081 << 15
	case ASRL:
		return 0x00089 << 15
	case ASRA:
		return 0x00091 << 15
	case AROTR:
		return 0x00099 << 15
	case AADDV:
		return 0x00b << 22
	case AADDVU:
		return 0x00b << 22

	case AJMP:
		return 0x14 << 26
	case AJAL,
		obj.ADUFFZERO,
		obj.ADUFFCOPY:
		return 0x15 << 26

	case AJIRL:
		return 0x13 << 26
	case ABLTU:
		return 0x1a << 26
	case ABLT, ABLTZ, ABGTZ:
		return 0x18 << 26
	case ABGEU:
		return 0x1b << 26
	case ABGE, ABGEZ, ABLEZ:
		return 0x19 << 26
	case -ABEQ: // beqz
		return 0x10 << 26
	case -ABNE: // bnez
		return 0x11 << 26
	case ABEQ:
		return 0x16 << 26
	case ABNE:
		return 0x17 << 26
	case ABFPT:
		return 0x12<<26 | 0x1<<8
	case ABFPF:
		return 0x12<<26 | 0x0<<8
	case APRELDX:
		return 0x07058 << 15 // preldx
	case AMOVB,
		AMOVBU:
		return 0x0a4 << 22
	case AMOVH,
		AMOVHU:
		return 0x0a5 << 22
	case AMOVW,
		AMOVWU:
		return 0x0a6 << 22
	case AMOVV:
		return 0x0a7 << 22
	case AMOVF:
		return 0x0ad << 22
	case AMOVD:
		return 0x0af << 22
	case -AMOVB:
		return 0x0a0 << 22
	case -AMOVBU:
		return 0x0a8 << 22
	case -AMOVH:
		return 0x0a1 << 22
	case -AMOVHU:
		return 0x0a9 << 22
	case -AMOVW:
		return 0x0a2 << 22
	case -AMOVWU:
		return 0x0aa << 22
	case -AMOVV:
		return 0x0a3 << 22
	case -AMOVF:
		return 0x0ac << 22
	case -AMOVD:
		return 0x0ae << 22
	case -AVMOVQ:
		return 0x0b0 << 22 // vld
	case -AXVMOVQ:
		return 0x0b2 << 22 // xvld
	case AVMOVQ:
		return 0x0b1 << 22 // vst
	case AXVMOVQ:
		return 0x0b3 << 22 // xvst
	case ASLLV:
		return 0x0041 << 16
	case ASRLV:
		return 0x0045 << 16
	case ASRAV:
		return 0x0049 << 16
	case AROTRV:
		return 0x004d << 16
	case -ALL:
		return 0x020 << 24
	case -ALLV:
		return 0x022 << 24
	case ASC:
		return 0x021 << 24
	case ASCV:
		return 0x023 << 24
	case AVANDB:
		return 0x1CF4 << 18 // vandi.b
	case AVORB:
		return 0x1CF5 << 18 // vori.b
	case AVXORB:
		return 0x1CF6 << 18 // xori.b
	case AVNORB:
		return 0x1CF7 << 18 // xnori.b
	case AXVANDB:
		return 0x1DF4 << 18 // xvandi.b
	case AXVORB:
		return 0x1DF5 << 18 // xvori.b
	case AXVXORB:
		return 0x1DF6 << 18 // xvxori.b
	case AXVNORB:
		return 0x1DF7 << 18 // xvnor.b
	case AVSEQB:
		return 0x0E500 << 15 //vseqi.b
	case AVSEQH:
		return 0x0E501 << 15 // vseqi.h
	case AVSEQW:
		return 0x0E502 << 15 //vseqi.w
	case AVSEQV:
		return 0x0E503 << 15 //vseqi.d
	case AXVSEQB:
		return 0x0ED00 << 15 //xvseqi.b
	case AXVSEQH:
		return 0x0ED01 << 15 // xvseqi.h
	case AXVSEQW:
		return 0x0ED02 << 15 // xvseqi.w
	case AXVSEQV:
		return 0x0ED03 << 15 // xvseqi.d
	case AVROTRB:
		return 0x1ca8<<18 | 0x1<<13 // vrotri.b
	case AVROTRH:
		return 0x1ca8<<18 | 0x1<<14 // vrotri.h
	case AVROTRW:
		return 0x1ca8<<18 | 0x1<<15 // vrotri.w
	case AVROTRV:
		return 0x1ca8<<18 | 0x1<<16 // vrotri.d
	case AXVROTRB:
		return 0x1da8<<18 | 0x1<<13 // xvrotri.b
	case AXVROTRH:
		return 0x1da8<<18 | 0x1<<14 // xvrotri.h
	case AXVROTRW:
		return 0x1da8<<18 | 0x1<<15 // xvrotri.w
	case AXVROTRV:
		return 0x1da8<<18 | 0x1<<16 // xvrotri.d
	case AVSLLB:
		return 0x1ccb<<18 | 0x1<<13 // vslli.b
	case AVSLLH:
		return 0x1ccb<<18 | 0x1<<14 // vslli.h
	case AVSLLW:
		return 0x1ccb<<18 | 0x1<<15 // vslli.w
	case AVSLLV:
		return 0x1ccb<<18 | 0x1<<16 // vslli.d
	case AVSRLB:
		return 0x1ccc<<18 | 0x1<<13 // vsrli.b
	case AVSRLH:
		return 0x1ccc<<18 | 0x1<<14 // vsrli.h
	case AVSRLW:
		return 0x1ccc<<18 | 0x1<<15 // vsrli.w
	case AVSRLV:
		return 0x1ccc<<18 | 0x1<<16 // vsrli.d
	case AVSRAB:
		return 0x1ccd<<18 | 0x1<<13 // vsrai.b
	case AVSRAH:
		return 0x1ccd<<18 | 0x1<<14 // vsrai.h
	case AVSRAW:
		return 0x1ccd<<18 | 0x1<<15 // vsrai.w
	case AVSRAV:
		return 0x1ccd<<18 | 0x1<<16 // vsrai.d
	case AXVSLLB:
		return 0x1dcb<<18 | 0x1<<13 // xvslli.b
	case AXVSLLH:
		return 0x1dcb<<18 | 0x1<<14 // xvslli.h
	case AXVSLLW:
		return 0x1dcb<<18 | 0x1<<15 // xvslli.w
	case AXVSLLV:
		return 0x1dcb<<18 | 0x1<<16 // xvslli.d
	case AXVSRLB:
		return 0x1dcc<<18 | 0x1<<13 // xvsrli.b
	case AXVSRLH:
		return 0x1dcc<<18 | 0x1<<14 // xvsrli.h
	case AXVSRLW:
		return 0x1dcc<<18 | 0x1<<15 // xvsrli.w
	case AXVSRLV:
		return 0x1dcc<<18 | 0x1<<16 // xvsrli.d
	case AXVSRAB:
		return 0x1dcd<<18 | 0x1<<13 // xvsrai.b
	case AXVSRAH:
		return 0x1dcd<<18 | 0x1<<14 // xvsrai.h
	case AXVSRAW:
		return 0x1dcd<<18 | 0x1<<15 // xvsrai.w
	case AXVSRAV:
		return 0x1dcd<<18 | 0x1<<16 // xvsrai.d
	case AVADDBU:
		return 0xe514 << 15 // vaddi.bu
	case AVADDHU:
		return 0xe515 << 15 // vaddi.hu
	case AVADDWU:
		return 0xe516 << 15 // vaddi.wu
	case AVADDVU:
		return 0xe517 << 15 // vaddi.du
	case AVSUBBU:
		return 0xe518 << 15 // vsubi.bu
	case AVSUBHU:
		return 0xe519 << 15 // vsubi.hu
	case AVSUBWU:
		return 0xe51a << 15 // vsubi.wu
	case AVSUBVU:
		return 0xe51b << 15 // vsubi.du
	case AXVADDBU:
		return 0xed14 << 15 // xvaddi.bu
	case AXVADDHU:
		return 0xed15 << 15 // xvaddi.hu
	case AXVADDWU:
		return 0xed16 << 15 // xvaddi.wu
	case AXVADDVU:
		return 0xed17 << 15 // xvaddi.du
	case AXVSUBBU:
		return 0xed18 << 15 // xvsubi.bu
	case AXVSUBHU:
		return 0xed19 << 15 // xvsubi.hu
	case AXVSUBWU:
		return 0xed1a << 15 // xvsubi.wu
	case AXVSUBVU:
		return 0xed1b << 15 // xvsubi.du
	case AVSHUF4IB:
		return 0x1ce4 << 18 // vshuf4i.b
	case AVSHUF4IH:
		return 0x1ce5 << 18 // vshuf4i.h
	case AVSHUF4IW:
		return 0x1ce6 << 18 // vshuf4i.w
	case AVSHUF4IV:
		return 0x1ce7 << 18 // vshuf4i.d
	case AXVSHUF4IB:
		return 0x1de4 << 18 // xvshuf4i.b
	case AXVSHUF4IH:
		return 0x1de5 << 18 // xvshuf4i.h
	case AXVSHUF4IW:
		return 0x1de6 << 18 // xvshuf4i.w
	case AXVSHUF4IV:
		return 0x1de7 << 18 // xvshuf4i.d
	}

	if a < 0 {
		c.ctxt.Diag("bad irr opcode -%v", -a)
	} else {
		c.ctxt.Diag("bad irr opcode %v", a)
	}
	return 0
}

func (c *ctxt0) opirir(a obj.As) uint32 {
	switch a {
	case ABSTRINSW:
		return 0x3<<21 | 0x0<<15 // bstrins.w
	case ABSTRINSV:
		return 0x2 << 22 // bstrins.d
	case ABSTRPICKW:
		return 0x3<<21 | 0x1<<15 // bstrpick.w
	case ABSTRPICKV:
		return 0x3 << 22 // bstrpick.d
	}

	return 0
}

func (c *ctxt0) opiir(a obj.As) uint32 {
	switch a {
	case APRELD:
		return 0x0AB << 22 // preld
	}

	return 0
}

func (c *ctxt0) specialFpMovInst(a obj.As, fclass int, tclass int) uint32 {
	switch a {
	case AMOVV:
		switch fclass {
		case C_REG:
			switch tclass {
			case C_FREG:
				return 0x452a << 10 // movgr2fr.d
			case C_FCCREG:
				return 0x4536 << 10 // movgr2cf
			case C_FCSRREG:
				return 0x4530 << 10 // movgr2fcsr
			}
		case C_FREG:
			switch tclass {
			case C_REG:
				return 0x452e << 10 // movfr2gr.d
			case C_FCCREG:
				return 0x4534 << 10 // movfr2cf
			}
		case C_FCCREG:
			switch tclass {
			case C_REG:
				return 0x4537 << 10 // movcf2gr
			case C_FREG:
				return 0x4535 << 10 // movcf2fr
			}
		case C_FCSRREG:
			switch tclass {
			case C_REG:
				return 0x4532 << 10 // movfcsr2gr
			}
		}

	case AMOVW:
		switch fclass {
		case C_REG:
			switch tclass {
			case C_FREG:
				return 0x4529 << 10 // movgr2fr.w
			}
		case C_FREG:
			switch tclass {
			case C_REG:
				return 0x452d << 10 // movfr2gr.s
			}
		}
	}

	c.ctxt.Diag("bad class combination: %s %s,%s\n", a, fclass, tclass)

	return 0
}

func (c *ctxt0) specialLsxMovInst(a obj.As, fReg, tReg int16) (op_code, index_mask uint32) {
	farng := (fReg >> EXT_TYPE_SHIFT) & EXT_TYPE_MASK
	tarng := (tReg >> EXT_TYPE_SHIFT) & EXT_TYPE_MASK
	fclass := c.rclass(fReg)
	tclass := c.rclass(tReg)

	switch fclass | (tclass << 16) {
	case C_REG | (C_ELEM << 16):
		// vmov Rn, Vd.<T>[index]
		switch a {
		case AVMOVQ:
			switch tarng {
			case ARNG_B:
				return (0x01CBAE << 14), 0xf // vinsgr2vr.b
			case ARNG_H:
				return (0x03975E << 13), 0x7 // vinsgr2vr.h
			case ARNG_W:
				return (0x072EBE << 12), 0x3 // vinsgr2vr.w
			case ARNG_V:
				return (0x0E5D7E << 11), 0x1 // vinsgr2vr.d
			}
		case AXVMOVQ:
			switch tarng {
			case ARNG_W:
				return (0x03B75E << 13), 0x7 // xvinsgr2vr.w
			case ARNG_V:
				return (0x076EBE << 12), 0x3 // xvinsgr2vr.d
			}
		}

	case C_ELEM | (C_REG << 16):
		// vmov Vd.<T>[index], Rn
		switch a {
		case AVMOVQ:
			switch farng {
			case ARNG_B:
				return (0x01CBBE << 14), 0xf // vpickve2gr.b
			case ARNG_H:
				return (0x03977E << 13), 0x7 // vpickve2gr.h
			case ARNG_W:
				return (0x072EFE << 12), 0x3 // vpickve2gr.w
			case ARNG_V:
				return (0x0E5DFE << 11), 0x1 // vpickve2gr.d
			case ARNG_BU:
				return (0x01CBCE << 14), 0xf // vpickve2gr.bu
			case ARNG_HU:
				return (0x03979E << 13), 0x7 // vpickve2gr.hu
			case ARNG_WU:
				return (0x072F3E << 12), 0x3 // vpickve2gr.wu
			case ARNG_VU:
				return (0x0E5E7E << 11), 0x1 // vpickve2gr.du
			}
		case AXVMOVQ:
			switch farng {
			case ARNG_W:
				return (0x03B77E << 13), 0x7 // xvpickve2gr.w
			case ARNG_V:
				return (0x076EFE << 12), 0x3 // xvpickve2gr.d
			case ARNG_WU:
				return (0x03B79E << 13), 0x7 // xvpickve2gr.wu
			case ARNG_VU:
				return (0x076F3E << 12), 0x3 // xvpickve2gr.du
			}
		}

	case C_REG | (C_ARNG << 16):
		// vmov Rn, Vd.<T>
		switch a {
		case AVMOVQ:
			switch tarng {
			case ARNG_16B:
				return (0x1CA7C0 << 10), 0x0 // vreplgr2vr.b
			case ARNG_8H:
				return (0x1CA7C1 << 10), 0x0 // vreplgr2vr.h
			case ARNG_4W:
				return (0x1CA7C2 << 10), 0x0 // vreplgr2vr.w
			case ARNG_2V:
				return (0x1CA7C3 << 10), 0x0 // vreplgr2vr.d
			}
		case AXVMOVQ:
			switch tarng {
			case ARNG_32B:
				return (0x1DA7C0 << 10), 0x0 // xvreplgr2vr.b
			case ARNG_16H:
				return (0x1DA7C1 << 10), 0x0 // xvreplgr2vr.h
			case ARNG_8W:
				return (0x1DA7C2 << 10), 0x0 // xvreplgr2vr.w
			case ARNG_4V:
				return (0x1DA7C3 << 10), 0x0 // xvreplgr2vr.d
			}
		}

	case C_XREG | (C_ARNG << 16):
		// vmov  xj, xd.<T>
		switch a {
		case AVMOVQ:
			return 0, 0 // unsupported op
		case AXVMOVQ:
			switch tarng {
			case ARNG_32B:
				return (0x1DC1C0 << 10), 0x0 // xvreplve0.b
			case ARNG_16H:
				return (0x1DC1E0 << 10), 0x0 // xvreplve0.h
			case ARNG_8W:
				return (0x1DC1F0 << 10), 0x0 // xvreplve0.w
			case ARNG_4V:
				return (0x1DC1F8 << 10), 0x0 // xvreplve0.d
			case ARNG_2Q:
				return (0x1DC1FC << 10), 0x0 // xvreplve0.q
			}
		}

	case C_XREG | (C_ELEM << 16):
		// vmov  xj, xd.<T>[index]
		switch a {
		case AVMOVQ:
			return 0, 0 // unsupported op
		case AXVMOVQ:
			switch tarng {
			case ARNG_W:
				return (0x03B7FE << 13), 0x7 // xvinsve0.w
			case ARNG_V:
				return (0x076FFE << 12), 0x3 // xvinsve0.d
			}
		}

	case C_ELEM | (C_XREG << 16):
		// vmov  xj.<T>[index], xd
		switch a {
		case AVMOVQ:
			return 0, 0 // unsupported op
		case AXVMOVQ:
			switch farng {
			case ARNG_W:
				return (0x03B81E << 13), 0x7 // xvpickve.w
			case ARNG_V:
				return (0x07703E << 12), 0x3 // xvpickve.d
			}
		}

	case C_ELEM | (C_ARNG << 16):
		// vmov  vj.<T>[index], vd.<T>
		switch a {
		case AVMOVQ:
			switch int32(farng) | (int32(tarng) << 16) {
			case int32(ARNG_B) | (int32(ARNG_16B) << 16):
				return (0x01CBDE << 14), 0xf // vreplvei.b
			case int32(ARNG_H) | (int32(ARNG_8H) << 16):
				return (0x0397BE << 13), 0x7 // vreplvei.h
			case int32(ARNG_W) | (int32(ARNG_4W) << 16):
				return (0x072F7E << 12), 0x3 // vreplvei.w
			case int32(ARNG_V) | (int32(ARNG_2V) << 16):
				return (0x0E5EFE << 11), 0x1 // vreplvei.d
			}
		case AXVMOVQ:
			return 0, 0 // unsupported op
		}
	}

	return 0, 0
}

func vshift(a obj.As) bool {
	switch a {
	case ASLLV,
		ASRLV,
		ASRAV,
		AROTRV:
		return true
	}
	return false
}
