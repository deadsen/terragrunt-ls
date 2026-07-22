package ast

import (
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"go.lsp.dev/protocol"
)

// FromHCLRange converts a hcl.Range to a LSP protocol.Range.
func FromHCLRange(source string, s hcl.Range) protocol.Range {
	return protocol.Range{
		Start: FromHCLPos(source, s.Start),
		End:   FromHCLPos(source, s.End),
	}
}

// FromHCLPos converts a hcl.Pos to a LSP protocol.Position.
func FromHCLPos(source string, s hcl.Pos) protocol.Position {
	line, _ := sourceLine(source, s.Line)
	byteColumn := max(s.Column-1, 0)
	byteColumn = min(byteColumn, len(line))
	for byteColumn > 0 && !utf8.ValidString(line[:byteColumn]) {
		byteColumn--
	}

	return protocol.Position{
		Line:      uint32(max(s.Line-1, 0)),
		Character: uint32(len(utf16.Encode([]rune(line[:byteColumn])))),
	}
}

// ToHCLRange converts a LSP protocol.Range to a hcl.Range.
func ToHCLRange(source string, s protocol.Range) hcl.Range {
	return hcl.Range{
		Filename: "",
		Start:    ToHCLPos(source, s.Start),
		End:      ToHCLPos(source, s.End),
	}
}

// ToHCLPos converts a LSP protocol.Position to a hcl.Pos.

func ToHCLPos(source string, s protocol.Position) hcl.Pos {
	line, lineStart := sourceLine(source, int(s.Line)+1)
	byteColumn := byteOffsetForUTF16Column(line, int(s.Character))

	return hcl.Pos{
		Line:   int(s.Line + 1),
		Column: byteColumn + 1,
		Byte:   lineStart + byteColumn,
	}
}

func sourceLine(source string, lineNumber int) (string, int) {
	lines := strings.Split(source, "\n")
	if lineNumber < 1 {
		lineNumber = 1
	}

	if lineNumber > len(lines) {
		return "", len(source)
	}

	start := 0
	for i := 0; i < lineNumber-1; i++ {
		start += len(lines[i]) + 1
	}

	return lines[lineNumber-1], start
}

func byteOffsetForUTF16Column(line string, column int) int {
	if column <= 0 {
		return 0
	}

	units := 0
	for byteOffset, r := range line {
		runeUnits := 1
		if r > 0xffff {
			runeUnits = 2
		}

		if units+runeUnits > column {
			return byteOffset
		}

		units += runeUnits
		if units == column {
			return byteOffset + utf8.RuneLen(r)
		}
	}

	return len(line)
}
