package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	vimKeyPaste = "paste"
)

type vimMode int

const (
	vimModeNormal vimMode = iota
	vimModeInsert
	vimModeCommand
)

type vimEditor struct {
	filename string
	rows     [][]rune
	cx       int
	cy       int
	rowoff   int
	coloff   int
	dirty    bool
	status   string
	reader   *bufio.Reader

	mode         vimMode
	yank         []rune
	yankLinewise bool
	pending      rune
	count        int
	cmdBuf       string
	pasteData    string
	quitting     bool
}

func vimEditFile(filename string) error {
	if filename == "" {
		return fmt.Errorf("file is required")
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return fmt.Errorf("vim requires an interactive terminal")
	}

	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), oldState)

	editor, err := newVimEditor(filename)
	if err != nil {
		return err
	}

	fmt.Print("\x1b[?1049h")
	fmt.Print("\x1b[?25l")
	fmt.Print("\x1b[?2004h")
	defer fmt.Print("\x1b[?2004l\x1b[?25h\x1b[?1049l")

	return editor.run()
}

func newVimEditor(filename string) (*vimEditor, error) {
	rows := [][]rune{{}}
	data, err := os.ReadFile(filename)
	if err == nil {
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		text = strings.TrimSuffix(text, "\n")
		parts := strings.Split(text, "\n")
		rows = make([][]rune, len(parts))
		for i, part := range parts {
			rows[i] = []rune(part)
		}
		if len(rows) == 0 {
			rows = [][]rune{{}}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	return &vimEditor{
		filename: filename,
		rows:     rows,
		mode:     vimModeNormal,
		status:   "-- NORMAL -- | :q quit | :w save | :42 goto line",
		reader:   bufio.NewReader(os.Stdin),
	}, nil
}

func (e *vimEditor) run() error {
	for !e.quitting {
		if err := e.refreshScreen(); err != nil {
			return err
		}
		key, err := e.readKey()
		if err != nil {
			return err
		}

		switch e.mode {
		case vimModeNormal:
			e.handleNormalKey(key)
		case vimModeInsert:
			e.handleInsertKey(key)
		case vimModeCommand:
			e.handleCommandKey(key)
		}
	}
	return nil
}

func (e *vimEditor) handleNormalKey(key string) {
	if key == vimKeyPaste {
		e.pasteAfter(e.pasteData)
		e.pasteData = ""
		return
	}

	if key == "\x11" || key == "\x03" {
		e.quit()
		return
	}
	if key == "\x13" {
		if err := e.save(); err != nil {
			e.status = fmt.Sprintf("save failed: %v", err)
		} else {
			e.status = "saved"
		}
		return
	}

	if len(key) == 1 {
		r := rune(key[0])
		if r >= '0' && r <= '9' {
			digit := int(r - '0')
			if e.count == 0 && digit == 0 && e.pending == 0 {
				e.cx = 0
				return
			}
			if e.count == 0 && digit == 0 && e.pending == 'g' {
				return
			}
			if e.count > 0 || digit > 0 {
				e.count = e.count*10 + digit
				return
			}
		}
	}

	switch key {
	case "i":
		e.enterInsert()
	case "a":
		if e.cx < len(e.rows[e.cy]) {
			e.cx++
		}
		e.enterInsert()
	case "I":
		e.cx = 0
		e.enterInsert()
	case "A":
		e.cx = len(e.rows[e.cy])
		e.enterInsert()
	case "o":
		e.openLineBelow()
		e.enterInsert()
	case "O":
		e.openLineAbove()
		e.enterInsert()
	case "h", keyArrowLeft:
		e.moveLeft()
	case "l", keyArrowRight:
		e.moveRight()
	case "k", keyArrowUp:
		e.moveUp()
	case "j", keyArrowDown:
		e.moveDown()
	case "0", keyHome:
		if e.pending == 'd' {
			e.deleteToStartOfLine()
			e.pending = 0
			return
		}
		e.cx = 0
	case "x":
		repeat := e.takeCount(1)
		for i := 0; i < repeat; i++ {
			e.deleteCharAtCursor()
		}
	case "D":
		e.deleteToEndOfLine()
	case "G":
		line := len(e.rows)
		if e.count > 0 {
			line = e.count
		}
		e.goToLine(line)
		e.count = 0
	case "d":
		if e.pending == 'd' {
			repeat := e.takeCount(1)
			for i := 0; i < repeat; i++ {
				e.deleteLine()
			}
			e.pending = 0
			return
		}
		e.pending = 'd'
	case "$", keyEnd:
		if e.pending == 'd' {
			e.deleteToEndOfLine()
			e.pending = 0
			return
		}
		e.cx = len(e.rows[e.cy])
	case "y":
		if e.pending == 'y' {
			repeat := e.takeCount(1)
			lines := make([][]rune, 0, repeat)
			for i := 0; i < repeat; i++ {
				if e.cy+i < len(e.rows) {
					lines = append(lines, append([]rune(nil), e.rows[e.cy+i]...))
				}
			}
			e.yankLines(lines)
			e.pending = 0
			return
		}
		e.pending = 'y'
	case "p":
		e.pasteAfter(e.yankText())
	case "P":
		e.pasteBefore(e.yankText())
	case "g":
		if e.pending == 'g' {
			e.goToLine(1)
			e.pending = 0
			e.count = 0
			return
		}
		e.pending = 'g'
	case ":":
		e.enterCommand()
	default:
		if len(key) == 1 {
			r := rune(key[0])
			if unicode.IsPrint(r) && r != 'g' && r != 'd' && r != 'y' {
				e.pending = 0
				e.count = 0
			}
		} else {
			e.pending = 0
			e.count = 0
		}
	}
}

func (e *vimEditor) handleInsertKey(key string) {
	if key == "\x1b" {
		e.enterNormal()
		return
	}
	if key == vimKeyPaste {
		e.insertBulk([]rune(e.pasteData))
		e.pasteData = ""
		return
	}
	if key == "\x13" {
		if err := e.save(); err != nil {
			e.status = fmt.Sprintf("save failed: %v", err)
		} else {
			e.status = "saved"
		}
		return
	}
	if key == "\x11" || key == "\x03" {
		e.quit()
		return
	}

	switch key {
	case "\r", "\n":
		e.insertNewline()
	case "\x7f", "\b":
		e.deleteBeforeCursor()
	case keyDelete:
		e.deleteAtCursor()
	case keyArrowLeft:
		e.moveLeft()
	case keyArrowRight:
		e.moveRight()
	case keyArrowUp:
		e.moveUp()
	case keyArrowDown:
		e.moveDown()
	case keyHome:
		e.cx = 0
	case keyEnd:
		e.cx = len(e.rows[e.cy])
	default:
		if len(key) > 0 {
			r, size := utf8.DecodeRuneInString(key)
			if r != utf8.RuneError || size > 1 {
				e.insertRune(r)
			}
		}
	}
}

func (e *vimEditor) handleCommandKey(key string) {
	switch key {
	case "\r", "\n":
		if err := e.executeCommand(e.cmdBuf); err != nil {
			e.status = err.Error()
		}
		e.enterNormal()
	case "\x1b":
		e.enterNormal()
	case "\x7f", "\b":
		if len(e.cmdBuf) > 0 {
			e.cmdBuf = e.cmdBuf[:len(e.cmdBuf)-1]
		}
	default:
		if len(key) == 1 {
			r := rune(key[0])
			if unicode.IsPrint(r) {
				e.cmdBuf += string(r)
			}
		}
	}
}

func (e *vimEditor) executeCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil
	}
	if cmd == "q" || cmd == "quit" {
		e.quit()
		return nil
	}
	if cmd == "w" || cmd == "write" {
		if err := e.save(); err != nil {
			return err
		}
		e.status = "saved"
		return nil
	}
	if cmd == "wq" {
		if err := e.save(); err != nil {
			return err
		}
		e.quit()
		return nil
	}
	if line, err := strconv.Atoi(cmd); err == nil {
		e.goToLine(line)
		return nil
	}
	return fmt.Errorf("unknown command: %s", cmd)
}

func (e *vimEditor) enterNormal() {
	e.mode = vimModeNormal
	e.pending = 0
	e.count = 0
	e.cmdBuf = ""
	e.status = "-- NORMAL -- | dd del line | D del to EOL | :42 goto | p paste"
}

func (e *vimEditor) enterInsert() {
	e.mode = vimModeInsert
	e.pending = 0
	e.count = 0
	e.status = "-- INSERT -- | Esc normal mode"
}

func (e *vimEditor) enterCommand() {
	e.mode = vimModeCommand
	e.cmdBuf = ""
	e.pending = 0
	e.count = 0
	e.status = ":"
}

func (e *vimEditor) takeCount(defaultCount int) int {
	if e.count > 0 {
		n := e.count
		e.count = 0
		return n
	}
	return defaultCount
}

func (e *vimEditor) goToLine(line int) {
	if line < 1 {
		line = 1
	}
	if line > len(e.rows) {
		line = len(e.rows)
	}
	e.cy = line - 1
	if e.cx > len(e.rows[e.cy]) {
		e.cx = len(e.rows[e.cy])
	}
}

func (e *vimEditor) deleteLine() {
	if len(e.rows) == 1 {
		e.yankLines([][]rune{append([]rune(nil), e.rows[0]...)})
		e.rows[0] = []rune{}
		e.cx = 0
		e.dirty = true
		return
	}
	e.yankLines([][]rune{append([]rune(nil), e.rows[e.cy]...)})
	e.rows = append(e.rows[:e.cy], e.rows[e.cy+1:]...)
	if e.cy >= len(e.rows) {
		e.cy = len(e.rows) - 1
	}
	if e.cx > len(e.rows[e.cy]) {
		e.cx = len(e.rows[e.cy])
	}
	e.dirty = true
}

func (e *vimEditor) deleteToStartOfLine() {
	row := e.rows[e.cy]
	if e.cx > 0 {
		e.yank = append([]rune(nil), row[:e.cx]...)
		e.yankLinewise = false
		e.rows[e.cy] = row[e.cx:]
		e.cx = 0
		e.dirty = true
	}
	e.pending = 0
	e.count = 0
}

func (e *vimEditor) deleteToEndOfLine() {
	row := e.rows[e.cy]
	if e.cx < len(row) {
		e.yank = append([]rune(nil), row[e.cx:]...)
		e.yankLinewise = false
		e.rows[e.cy] = row[:e.cx]
		e.dirty = true
	}
	e.pending = 0
	e.count = 0
}

func (e *vimEditor) deleteCharAtCursor() {
	row := e.rows[e.cy]
	if e.cx < len(row) {
		e.rows[e.cy] = append(row[:e.cx], row[e.cx+1:]...)
		e.dirty = true
		return
	}
	if e.cy+1 < len(e.rows) {
		e.rows[e.cy] = append(e.rows[e.cy], e.rows[e.cy+1]...)
		e.rows = append(e.rows[:e.cy+1], e.rows[e.cy+2:]...)
		e.dirty = true
	}
}

func (e *vimEditor) yankLines(lines [][]rune) {
	if len(lines) == 1 {
		e.yank = append([]rune(nil), lines[0]...)
		e.yankLinewise = true
		return
	}
	var buf strings.Builder
	for i, line := range lines {
		if i > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(string(line))
	}
	e.yank = []rune(buf.String())
	e.yankLinewise = true
}

func (e *vimEditor) yankText() string {
	return string(e.yank)
}

func (e *vimEditor) pasteAfter(text string) {
	if text == "" {
		return
	}
	if e.yankLinewise && !strings.Contains(text, "\n") {
		e.pasteLinewiseAfter()
		return
	}
	e.insertBulk([]rune(strings.ReplaceAll(text, "\r\n", "\n")))
}

func (e *vimEditor) pasteBefore(text string) {
	if text == "" {
		return
	}
	if e.yankLinewise && !strings.Contains(text, "\n") {
		e.pasteLinewiseBefore()
		return
	}
	e.insertBulk([]rune(strings.ReplaceAll(text, "\r\n", "\n")))
}

func (e *vimEditor) pasteLinewiseAfter() {
	line := append([]rune(nil), e.yank...)
	e.rows = append(e.rows, nil)
	copy(e.rows[e.cy+2:], e.rows[e.cy+1:])
	e.rows[e.cy+1] = line
	e.cy++
	e.cx = 0
	e.dirty = true
}

func (e *vimEditor) pasteLinewiseBefore() {
	line := append([]rune(nil), e.yank...)
	e.rows = append(e.rows, nil)
	copy(e.rows[e.cy+1:], e.rows[e.cy:])
	e.rows[e.cy] = line
	e.cx = 0
	e.dirty = true
}

func (e *vimEditor) insertBulk(text []rune) {
	if len(text) == 0 {
		return
	}
	lines := splitRunesLines(text)
	first := lines[0]
	row := e.rows[e.cy]
	left := append(append([]rune(nil), row[:e.cx]...), first...)
	right := append([]rune(nil), row[e.cx:]...)
	e.rows[e.cy] = left
	e.cx = len(left)

	for i := 1; i < len(lines); i++ {
		e.rows = append(e.rows, nil)
		copy(e.rows[e.cy+2:], e.rows[e.cy+1:])
		e.rows[e.cy+1] = lines[i]
		e.cy++
		e.cx = len(lines[i])
	}
	if len(lines) > 1 {
		e.rows[e.cy] = append(e.rows[e.cy], right...)
		e.cx = len(e.rows[e.cy]) - len(right)
	} else {
		e.rows[e.cy] = append(e.rows[e.cy], right...)
		e.cx = len(left)
	}
	e.dirty = true
}

func splitRunesLines(text []rune) [][]rune {
	lines := make([][]rune, 0, 4)
	start := 0
	for i, r := range text {
		if r == '\n' {
			lines = append(lines, text[start:i])
			start = i + 1
		}
	}
	lines = append(lines, text[start:])
	return lines
}

func (e *vimEditor) openLineBelow() {
	e.rows = append(e.rows, nil)
	copy(e.rows[e.cy+2:], e.rows[e.cy+1:])
	e.rows[e.cy+1] = []rune{}
	e.cy++
	e.cx = 0
	e.dirty = true
}

func (e *vimEditor) openLineAbove() {
	e.rows = append(e.rows, nil)
	copy(e.rows[e.cy+1:], e.rows[e.cy:])
	e.rows[e.cy] = []rune{}
	e.cx = 0
	e.dirty = true
}

func (e *vimEditor) insertRune(r rune) {
	if r < 32 || r == 127 {
		return
	}
	row := e.rows[e.cy]
	row = append(row, 0)
	copy(row[e.cx+1:], row[e.cx:])
	row[e.cx] = r
	e.rows[e.cy] = row
	e.cx++
	e.dirty = true
}

func (e *vimEditor) insertNewline() {
	row := e.rows[e.cy]
	left := append([]rune(nil), row[:e.cx]...)
	right := append([]rune(nil), row[e.cx:]...)
	e.rows[e.cy] = left
	e.rows = append(e.rows, nil)
	copy(e.rows[e.cy+2:], e.rows[e.cy+1:])
	e.rows[e.cy+1] = right
	e.cy++
	e.cx = 0
	e.dirty = true
}

func (e *vimEditor) deleteBeforeCursor() {
	if e.cy == 0 && e.cx == 0 {
		return
	}
	if e.cx > 0 {
		row := e.rows[e.cy]
		e.rows[e.cy] = append(row[:e.cx-1], row[e.cx:]...)
		e.cx--
		e.dirty = true
		return
	}
	previousLen := len(e.rows[e.cy-1])
	e.rows[e.cy-1] = append(e.rows[e.cy-1], e.rows[e.cy]...)
	e.rows = append(e.rows[:e.cy], e.rows[e.cy+1:]...)
	e.cy--
	e.cx = previousLen
	e.dirty = true
}

func (e *vimEditor) deleteAtCursor() {
	row := e.rows[e.cy]
	if e.cx < len(row) {
		e.rows[e.cy] = append(row[:e.cx], row[e.cx+1:]...)
		e.dirty = true
		return
	}
	if e.cy+1 < len(e.rows) {
		e.rows[e.cy] = append(e.rows[e.cy], e.rows[e.cy+1]...)
		e.rows = append(e.rows[:e.cy+1], e.rows[e.cy+2:]...)
		e.dirty = true
	}
}

func (e *vimEditor) moveLeft() {
	if e.cx > 0 {
		e.cx--
	} else if e.cy > 0 {
		e.cy--
		e.cx = len(e.rows[e.cy])
	}
}

func (e *vimEditor) moveRight() {
	if e.cx < len(e.rows[e.cy]) {
		e.cx++
	} else if e.cy+1 < len(e.rows) {
		e.cy++
		e.cx = 0
	}
}

func (e *vimEditor) moveUp() {
	if e.cy > 0 {
		e.cy--
		if e.cx > len(e.rows[e.cy]) {
			e.cx = len(e.rows[e.cy])
		}
	}
}

func (e *vimEditor) moveDown() {
	if e.cy+1 < len(e.rows) {
		e.cy++
		if e.cx > len(e.rows[e.cy]) {
			e.cx = len(e.rows[e.cy])
		}
	}
}

func (e *vimEditor) refreshScreen() error {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return err
	}
	if width < 20 || height < 5 {
		return fmt.Errorf("terminal is too small")
	}
	editorRows := height - 2
	lineNumberWidth := e.lineNumberWidth()
	textWidth := width - lineNumberWidth
	if textWidth < 1 {
		return fmt.Errorf("terminal is too narrow")
	}
	e.scroll(textWidth, editorRows)

	var out strings.Builder
	out.Grow((width + 8) * (editorRows + 2))
	out.WriteString("\x1b[?25l")
	out.WriteString("\x1b[H")
	for y := 0; y < editorRows; y++ {
		fileRow := y + e.rowoff
		out.WriteString("\x1b[K")
		if fileRow >= len(e.rows) {
			e.writeLineNumber(&out, fileRow, lineNumberWidth, false)
			out.WriteByte('~')
		} else {
			e.writeLineNumber(&out, fileRow, lineNumberWidth, true)
			row := e.rows[fileRow]
			if e.coloff < len(row) {
				visible := row[e.coloff:]
				if len(visible) > textWidth {
					visible = visible[:textWidth]
				}
				out.WriteString(string(visible))
			}
		}
		out.WriteString("\r\n")
	}

	e.writeStatusLine(&out, width)
	out.WriteString("\r\n")
	e.writeMessageLine(&out, width)

	cursorY := e.cy - e.rowoff + 1
	cursorX := lineNumberWidth + e.cx - e.coloff + 1
	out.WriteString(fmt.Sprintf("\x1b[%d;%dH", cursorY, cursorX))
	out.WriteString("\x1b[?25h")
	_, err = os.Stdout.WriteString(out.String())
	return err
}

func (e *vimEditor) lineNumberWidth() int {
	return len(strconv.Itoa(len(e.rows))) + 2
}

func (e *vimEditor) writeLineNumber(out *strings.Builder, row int, width int, exists bool) {
	if exists {
		fmt.Fprintf(out, "\x1b[2m%*d \x1b[m", width-1, row+1)
		return
	}
	out.WriteString(strings.Repeat(" ", width-2))
	out.WriteString("\x1b[2m~ \x1b[m")
}

func (e *vimEditor) writeStatusLine(out *strings.Builder, width int) {
	name := e.filename
	if name == "" {
		name = "[No Name]"
	}
	dirty := ""
	if e.dirty {
		dirty = " *"
	}
	mode := "NORMAL"
	switch e.mode {
	case vimModeInsert:
		mode = "INSERT"
	case vimModeCommand:
		mode = "CMD"
	}
	left := fmt.Sprintf(" %s [%s]%s", name, mode, dirty)
	right := fmt.Sprintf(" %d:%d ", e.cy+1, e.cx+1)
	padding := width - len([]rune(left)) - len([]rune(right))
	if padding < 1 {
		padding = 1
	}
	status := left + strings.Repeat(" ", padding) + right
	runes := []rune(status)
	if len(runes) > width {
		runes = runes[:width]
	}
	out.WriteString("\x1b[7m")
	out.WriteString(string(runes))
	out.WriteString(strings.Repeat(" ", width-len(runes)))
	out.WriteString("\x1b[m")
}

func (e *vimEditor) writeMessageLine(out *strings.Builder, width int) {
	out.WriteString("\x1b[K")
	message := []rune(e.status)
	if e.mode == vimModeCommand {
		message = append([]rune(":"), []rune(e.cmdBuf)...)
	}
	if len(message) > width {
		message = message[:width]
	}
	out.WriteString(string(message))
}

func (e *vimEditor) scroll(width int, editorRows int) {
	if e.cy < e.rowoff {
		e.rowoff = e.cy
	}
	if e.cy >= e.rowoff+editorRows {
		e.rowoff = e.cy - editorRows + 1
	}
	if e.cx < e.coloff {
		e.coloff = e.cx
	}
	if e.cx >= e.coloff+width {
		e.coloff = e.cx - width + 1
	}
}

func (e *vimEditor) readKey() (string, error) {
	r, _, err := e.reader.ReadRune()
	if err != nil {
		return "", err
	}
	if r != '\x1b' {
		return string(r), nil
	}

	next, _, err := e.reader.ReadRune()
	if err != nil {
		return "\x1b", nil
	}
	if next == 'O' {
		code, _, err := e.reader.ReadRune()
		if err != nil {
			return "\x1b", nil
		}
		switch code {
		case 'A':
			return keyArrowUp, nil
		case 'B':
			return keyArrowDown, nil
		case 'C':
			return keyArrowRight, nil
		case 'D':
			return keyArrowLeft, nil
		case 'H':
			return keyHome, nil
		case 'F':
			return keyEnd, nil
		default:
			return "\x1b", nil
		}
	}
	if next != '[' {
		if err := e.reader.UnreadRune(); err != nil {
			return "", err
		}
		return "\x1b", nil
	}

	var sequence []rune
	for {
		code, _, err := e.reader.ReadRune()
		if err != nil {
			return "\x1b", nil
		}
		sequence = append(sequence, code)
		if code == '~' {
			break
		}
		if unicode.IsLetter(code) && !(len(sequence) >= 2 && sequence[len(sequence)-2] == ';') {
			break
		}
	}

	if paste, ok := e.parseBracketedPaste(sequence); ok {
		e.pasteData = paste
		return vimKeyPaste, nil
	}
	return parseCSISequence(sequence), nil
}

func (e *vimEditor) parseBracketedPaste(sequence []rune) (string, bool) {
	if len(sequence) < 4 {
		return "", false
	}
	if string(sequence[:3]) != "200" || sequence[3] != '~' {
		return "", false
	}

	var pasted strings.Builder
	for {
		r, _, err := e.reader.ReadRune()
		if err != nil {
			break
		}
		if r == '\x1b' {
			end, _, err := e.reader.ReadRune()
			if err != nil {
				break
			}
			if end != '[' {
				pasted.WriteRune(r)
				pasted.WriteRune(end)
				continue
			}
			var tail []rune
			for {
				code, _, err := e.reader.ReadRune()
				if err != nil {
					return pasted.String(), true
				}
				tail = append(tail, code)
				if code == '~' {
					break
				}
			}
			if string(tail) == "201~" {
				return pasted.String(), true
			}
			pasted.WriteRune(r)
			pasted.WriteRune('[')
			for _, c := range tail {
				pasted.WriteRune(c)
			}
			continue
		}
		pasted.WriteRune(r)
	}
	return pasted.String(), true
}

func (e *vimEditor) save() error {
	lines := make([]string, len(e.rows))
	for i, row := range e.rows {
		lines[i] = string(row)
	}
	data := strings.Join(lines, "\n")
	if err := os.WriteFile(e.filename, []byte(data), 0644); err != nil {
		return err
	}
	e.dirty = false
	return nil
}

func (e *vimEditor) quit() {
	e.quitting = true
}
