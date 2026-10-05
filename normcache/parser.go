package normcache

import (
	"fmt"
	"strings"
)

// VarRef 表示参数中的变量引用，读写时用变量表解析为具体值。
type VarRef struct {
	Name string `json:"name"`
}

type lexer struct {
	src string
	pos int
}

func (l *lexer) skipWS() {
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == ',' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			l.pos++
			continue
		}
		if c == '#' {
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			continue
		}
		break
	}
}

func (l *lexer) peek() byte {
	l.skipWS()
	if l.pos >= len(l.src) {
		return 0
	}
	return l.src[l.pos]
}

func (l *lexer) next() byte {
	c := l.peek()
	if c != 0 {
		l.pos++
	}
	return c
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
func isNameChar(c byte) bool {
	return isNameStart(c) || (c >= '0' && c <= '9')
}

func (l *lexer) name() (string, error) {
	l.skipWS()
	if l.pos >= len(l.src) || !isNameStart(l.src[l.pos]) {
		return "", fmt.Errorf("expected name at offset %d", l.pos)
	}
	start := l.pos
	for l.pos < len(l.src) && isNameChar(l.src[l.pos]) {
		l.pos++
	}
	return l.src[start:l.pos], nil
}

func (l *lexer) expect(c byte) error {
	if l.next() != c {
		return fmt.Errorf("expected %q at offset %d", c, l.pos)
	}
	return nil
}

// value 解析参数字面量：字符串、数字、布尔、null、变量、列表、对象。
func (l *lexer) value() (any, error) {
	l.skipWS()
	c := l.peek()
	switch {
	case c == '$':
		l.pos++
		n, err := l.name()
		if err != nil {
			return nil, err
		}
		return VarRef{Name: n}, nil
	case c == '"':
		l.pos++
		var sb strings.Builder
		for l.pos < len(l.src) && l.src[l.pos] != '"' {
			if l.src[l.pos] == '\\' && l.pos+1 < len(l.src) {
				l.pos++
			}
			sb.WriteByte(l.src[l.pos])
			l.pos++
		}
		if err := l.expect('"'); err != nil {
			return nil, err
		}
		return sb.String(), nil
	case c == '[':
		l.pos++
		var list []any
		for l.peek() != ']' {
			v, err := l.value()
			if err != nil {
				return nil, err
			}
			list = append(list, v)
		}
		l.pos++
		return list, nil
	case c == '{':
		l.pos++
		obj := map[string]any{}
		for l.peek() != '}' {
			k, err := l.name()
			if err != nil {
				return nil, err
			}
			if err := l.expect(':'); err != nil {
				return nil, err
			}
			v, err := l.value()
			if err != nil {
				return nil, err
			}
			obj[k] = v
		}
		l.pos++
		return obj, nil
	case c == '-' || (c >= '0' && c <= '9'):
		start := l.pos
		isFloat := false
		for l.pos < len(l.src) {
			ch := l.src[l.pos]
			if ch == '.' || ch == 'e' || ch == 'E' {
				isFloat = true
			}
			if !isNameChar(ch) && ch != '-' && ch != '+' && ch != '.' {
				break
			}
			l.pos++
		}
		tok := l.src[start:l.pos]
		if isFloat {
			var f float64
			if _, err := fmt.Sscanf(tok, "%g", &f); err != nil {
				return nil, fmt.Errorf("bad number %q", tok)
			}
			return f, nil
		}
		var i int64
		if _, err := fmt.Sscanf(tok, "%d", &i); err != nil {
			return nil, fmt.Errorf("bad number %q", tok)
		}
		return i, nil
	default:
		n, err := l.name()
		if err != nil {
			return nil, err
		}
		switch n {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "null":
			return nil, nil
		}
		return nil, fmt.Errorf("unexpected token %q", n)
	}
}

func (l *lexer) args() (map[string]any, error) {
	if l.peek() != '(' {
		return nil, nil
	}
	l.pos++
	args := map[string]any{}
	for l.peek() != ')' {
		k, err := l.name()
		if err != nil {
			return nil, err
		}
		if err := l.expect(':'); err != nil {
			return nil, err
		}
		v, err := l.value()
		if err != nil {
			return nil, err
		}
		args[k] = v
	}
	l.pos++
	return args, nil
}

func (l *lexer) selectionSet() (SelectionSet, error) {
	if err := l.expect('{'); err != nil {
		return SelectionSet{}, err
	}
	var set SelectionSet
	for l.peek() != '}' {
		if l.peek() == 0 {
			return SelectionSet{}, fmt.Errorf("unexpected EOF in selection set")
		}
		sel, err := l.selection()
		if err != nil {
			return SelectionSet{}, err
		}
		set.Selections = append(set.Selections, sel)
	}
	l.pos++
	return set, nil
}

func (l *lexer) selection() (Selection, error) {
	l.skipWS()
	if l.pos+2 < len(l.src) && l.src[l.pos:l.pos+3] == "..." {
		l.pos += 3
		kw, err := l.name()
		if err != nil {
			return Selection{}, err
		}
		if kw != "on" {
			return Selection{}, fmt.Errorf("only inline fragments (... on Type) are supported, got %q", kw)
		}
		typ, err := l.name()
		if err != nil {
			return Selection{}, err
		}
		set, err := l.selectionSet()
		if err != nil {
			return Selection{}, err
		}
		return Selection{Fragment: &InlineFragment{TypeCondition: typ, Selection: set}}, nil
	}
	n1, err := l.name()
	if err != nil {
		return Selection{}, err
	}
	f := &Field{Name: n1}
	// 别名：alias: field
	if l.peek() == ':' {
		l.pos++
		n2, err := l.name()
		if err != nil {
			return Selection{}, err
		}
		f.Alias = n1
		f.Name = n2
	}
	args, err := l.args()
	if err != nil {
		return Selection{}, err
	}
	f.Args = args
	if l.peek() == '{' {
		set, err := l.selectionSet()
		if err != nil {
			return Selection{}, err
		}
		f.Selection = set
	}
	return Selection{Field: f}, nil
}

// parseQuery 解析查询文本。支持可选的 `query Name($v: T)` 头部。
func parseQuery(src string) (SelectionSet, error) {
	l := &lexer{src: src}
	l.skipWS()
	// 跳过可选的操作头：query/mutation 关键字、操作名、变量定义
	if l.pos < len(l.src) && isNameStart(l.src[l.pos]) {
		save := l.pos
		kw, _ := l.name()
		if kw == "query" || kw == "mutation" || kw == "subscription" {
			if isNameStart(l.peek()) {
				if _, err := l.name(); err != nil {
					return SelectionSet{}, err
				}
			}
			if l.peek() == '(' {
				depth := 0
				for l.pos < len(l.src) {
					c := l.src[l.pos]
					l.pos++
					if c == '(' {
						depth++
					} else if c == ')' {
						depth--
						if depth == 0 {
							break
						}
					}
				}
			}
		} else {
			l.pos = save
		}
	}
	set, err := l.selectionSet()
	if err != nil {
		return SelectionSet{}, err
	}
	l.skipWS()
	if l.pos != len(l.src) {
		return SelectionSet{}, fmt.Errorf("trailing input at offset %d", l.pos)
	}
	return set, nil
}
