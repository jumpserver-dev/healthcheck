package main

import (
	"bufio"
	"strings"
	"testing"
)

func TestVimSplitRunesLines(t *testing.T) {
	lines := splitRunesLines([]rune("a\nb\nc"))
	if len(lines) != 3 || string(lines[0]) != "a" || string(lines[1]) != "b" || string(lines[2]) != "c" {
		t.Fatalf("unexpected lines: %#v", lines)
	}
}

func TestVimInsertBulkSingleLine(t *testing.T) {
	editor := &vimEditor{rows: [][]rune{[]rune("hello")}, cx: 5, cy: 0}
	editor.insertBulk([]rune(" world"))
	if string(editor.rows[0]) != "hello world" || editor.cx != 11 {
		t.Fatalf("unexpected row %q cursor %d", editor.rows[0], editor.cx)
	}
}

func TestVimInsertBulkMultiline(t *testing.T) {
	editor := &vimEditor{rows: [][]rune{[]rune("ab")}, cx: 1, cy: 0}
	editor.insertBulk([]rune("X\nY"))
	if len(editor.rows) != 2 || string(editor.rows[0]) != "aX" || string(editor.rows[1]) != "Yb" {
		t.Fatalf("unexpected rows: %#v", editor.rows)
	}
}

func TestVimGoToLine(t *testing.T) {
	editor := &vimEditor{rows: [][]rune{{}, {}, {}}, cy: 0, cx: 0}
	editor.goToLine(2)
	if editor.cy != 1 {
		t.Fatalf("unexpected line: %d", editor.cy)
	}
}

func TestVimExecuteCommandGoto(t *testing.T) {
	editor := &vimEditor{rows: [][]rune{{}, {}, {}}, cy: 0}
	if err := editor.executeCommand("3"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if editor.cy != 2 {
		t.Fatalf("unexpected line: %d", editor.cy)
	}
}

func TestVimDeleteLine(t *testing.T) {
	editor := &vimEditor{rows: [][]rune{[]rune("one"), []rune("two")}, cy: 0}
	editor.deleteLine()
	if len(editor.rows) != 1 || string(editor.rows[0]) != "two" || string(editor.yank) != "one" {
		t.Fatalf("unexpected state rows=%#v yank=%q", editor.rows, editor.yank)
	}
}

func TestVimDeleteToEndOfLine(t *testing.T) {
	editor := &vimEditor{rows: [][]rune{[]rune("hello")}, cx: 2}
	editor.deleteToEndOfLine()
	if string(editor.rows[0]) != "he" || string(editor.yank) != "llo" {
		t.Fatalf("unexpected state row=%q yank=%q", editor.rows[0], editor.yank)
	}
}

func TestVimParseBracketedPaste(t *testing.T) {
	editor := &vimEditor{reader: bufio.NewReader(strings.NewReader("hello\x1b[201~tail"))}
	paste, ok := editor.parseBracketedPaste([]rune("200~"))
	if !ok || paste != "hello" {
		t.Fatalf("unexpected paste: ok=%v paste=%q", ok, paste)
	}
}

func TestVimReadKeyBracketedPaste(t *testing.T) {
	editor := &vimEditor{reader: bufio.NewReader(strings.NewReader("\x1b[200~paste\x1b[201~"))}
	key, err := editor.readKey()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != vimKeyPaste || editor.pasteData != "paste" {
		t.Fatalf("unexpected key=%q paste=%q", key, editor.pasteData)
	}
}
