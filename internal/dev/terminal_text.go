package dev

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// terminalText removes terminal commands from untrusted task text, filenames and
// worker output. OSC (including clipboard/title commands), CSI, DCS and C1
// controls must never reach the user's terminal through a read-only dashboard.
func terminalText(text string) string {
	var out strings.Builder
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			index++
			if index >= len(text) {
				break
			}
			switch text[index] {
			case '[':
				index++
				for index < len(text) {
					b := text[index]
					index++
					if b >= 0x40 && b <= 0x7e {
						break
					}
				}
			case ']', 'P', '^', '_', 'X':
				index++
				for index < len(text) {
					if text[index] == 7 {
						index++
						break
					}
					if text[index] == 0x1b && index+1 < len(text) && text[index+1] == '\\' {
						index += 2
						break
					}
					index++
				}
			default:
				// Escape sequences use optional intermediate bytes and one final byte.
				for index < len(text) && text[index] >= 0x20 && text[index] <= 0x2f {
					index++
				}
				if index < len(text) {
					index++
				}
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(text[index:])
		index += size
		if r == '\n' {
			out.WriteRune(r)
			continue
		}
		if r == '\t' {
			out.WriteString("  ")
			continue
		}
		if unicode.IsControl(r) || r == 0x202a || r == 0x202b || r == 0x202c || r == 0x202d || r == 0x202e || (r >= 0x2066 && r <= 0x2069) {
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// cellWidth covers combining marks, East Asian wide characters and common emoji.
// Joined emoji may be overestimated, which clips early rather than overflowing.
func runeCells(r rune) int {
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) || (r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) || (r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6f) || (r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) || (r >= 0x1f000 && r <= 0x1faff) ||
		(r >= 0x20000 && r <= 0x3fffd)) {
		return 2
	}
	return 1
}

func cells(text string) int {
	total := 0
	for _, r := range terminalText(text) {
		total += runeCells(r)
	}
	return total
}

func takeCells(text string, width int) (string, string) {
	used := 0
	for index, r := range text {
		if used+runeCells(r) > width {
			return text[:index], text[index:]
		}
		used += runeCells(r)
	}
	return text, ""
}

func fit(text string, width int) string {
	if width <= 0 {
		return ""
	}
	text = strings.ReplaceAll(terminalText(text), "\n", " ")
	if size := cells(text); size > width {
		head, _ := takeCells(text, width-1)
		return head + "…" + strings.Repeat(" ", max(0, width-1-cells(head)))
	} else {
		return text + strings.Repeat(" ", width-size)
	}
}

func wrap(text string, width int) []string {
	if width <= 0 {
		return nil
	}
	var out []string
	for _, paragraph := range strings.Split(terminalText(text), "\n") {
		line := ""
		for _, word := range strings.Fields(paragraph) {
			for cells(word) > width {
				if line != "" {
					out, line = append(out, line), ""
				}
				head, tail := takeCells(word, width)
				if head == "" {
					_, size := utf8.DecodeRuneInString(word)
					head, tail = "…", word[size:]
				}
				out, word = append(out, head), tail
			}
			if line != "" && cells(line)+1+cells(word) > width {
				out, line = append(out, line), ""
			}
			if line != "" && word != "" {
				line += " "
			}
			line += word
		}
		out = append(out, line)
	}
	return out
}

// clipStyled retains only our small SGR palette, strips other terminal escapes,
// and limits a frame row in display cells. It never lets a wide row wrap and
// corrupt the next screen row.
func clipStyled(text string, width int) string {
	if width <= 0 {
		return ""
	}
	var out strings.Builder
	used, styled := 0, false
	for index := 0; index < len(text); {
		if text[index] == 0x1b {
			end := index + 2
			if index+1 < len(text) && text[index+1] == '[' {
				for end < len(text) && (text[end] >= '0' && text[end] <= '9' || text[end] == ';') {
					end++
				}
				if end < len(text) && text[end] == 'm' {
					sequence := text[index : end+1]
					switch sequence {
					case "\x1b[0m", "\x1b[1m", "\x1b[2m", "\x1b[31m", "\x1b[32m", "\x1b[33m", "\x1b[36m":
						out.WriteString(sequence)
						styled = true
					}
					index = end + 1
					continue
				}
			}
			// This path is a final defensive guard; model values are already
			// sanitized before they are styled by paint.
			text = text[:index] + terminalText(text[index:])
			continue
		}
		r, size := utf8.DecodeRuneInString(text[index:])
		index += size
		if unicode.IsControl(r) {
			continue
		}
		if used+runeCells(r) > width {
			break
		}
		out.WriteRune(r)
		used += runeCells(r)
	}
	if styled {
		out.WriteString("\x1b[0m")
	}
	return out.String()
}
