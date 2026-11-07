package terminal

import (
	"strconv"
	"unicode"

	tea "charm.land/bubbletea/v2"
	g "go.mitchellh.com/libghostty"
)

// Translate key identities, not wire sequences. Ghostty owns encoding, including
// application cursor mode, modifiers, and the negotiated keyboard protocol.
func logicalKey(code rune) g.Key {
	if key, ok := namedKeys[code]; ok {
		return key
	}
	code = unicode.ToLower(code)
	var name string
	switch {
	case code >= 'a' && code <= 'z':
		name = "key_" + string(code)
	case code >= '0' && code <= '9':
		name = "digit_" + string(code)
	case code >= tea.KeyF1 && code <= tea.KeyF35:
		name = "f" + strconv.Itoa(int(code-tea.KeyF1)+1)
	default:
		return g.KeyUnidentified
	}
	key, err := g.ParseKey(name)
	if err != nil {
		return g.KeyUnidentified
	}
	return key
}

var namedKeys = map[rune]g.Key{
	tea.KeyEnter: g.KeyEnter, tea.KeyEscape: g.KeyEscape, tea.KeyTab: g.KeyTab,
	tea.KeyBackspace: g.KeyBackspace, tea.KeySpace: g.KeySpace,
	tea.KeyUp: g.KeyArrowUp, tea.KeyDown: g.KeyArrowDown, tea.KeyLeft: g.KeyArrowLeft, tea.KeyRight: g.KeyArrowRight,
	tea.KeyHome: g.KeyHome, tea.KeyEnd: g.KeyEnd, tea.KeyPgUp: g.KeyPageUp, tea.KeyPgDown: g.KeyPageDown,
	tea.KeyDelete: g.KeyDelete, tea.KeyInsert: g.KeyInsert,
	'`': g.KeyBackquote, '\\': g.KeyBackslash, '[': g.KeyBracketLeft, ']': g.KeyBracketRight,
	',': g.KeyComma, '.': g.KeyPeriod, '/': g.KeySlash, ';': g.KeySemicolon, '\'': g.KeyQuote,
	'-': g.KeyMinus, '=': g.KeyEqual,
}
