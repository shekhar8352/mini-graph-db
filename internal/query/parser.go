package query

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/shekhar8352/mini-graph-db/internal/gerr"
)

// Parser is a one-lookahead recursive-descent parser.
type Parser struct {
	lx  *Lexer
	cur Token
}

// Parse lexes and parses one statement from src.
func Parse(src string) (Stmt, error) {
	p := &Parser{lx: NewLexer(src)}
	p.advance()
	if p.cur.Type == TokenError {
		return nil, errOf(p.cur)
	}
	if p.cur.Type == TokenEOF {
		return nil, gerr.New(gerr.Syntax, "empty statement")
	}
	if p.cur.Type != TokenIdent {
		return nil, gerr.Newf(gerr.Syntax, "expected command, got %s", p.cur.Type)
	}
	kw := strings.ToUpper(p.cur.Lexeme)
	p.advance()

	var stmt Stmt
	var err error
	switch kw {
	case "CREATE":
		stmt, err = p.parseCreate()
	case "UPDATE":
		stmt, err = p.parseUpdate()
	case "MATCH":
		stmt, err = p.parseMatch()
	case "EDGES":
		stmt, err = p.parseEdges()
	case "NEIGHBORS":
		stmt, err = p.parseNeighbors()
	case "PATH":
		stmt, err = p.parsePath()
	case "DELETE":
		stmt, err = p.parseDelete()
	case "GET":
		stmt, err = p.parseGet()
	case "SHOW":
		stmt, err = p.parseShow()
	case "HELP":
		stmt = HelpStmt{}
	case "EXIT", "QUIT":
		stmt = ExitStmt{}
	case "SAVE":
		stmt, err = p.parseSave()
	case "LOAD":
		stmt, err = p.parseLoad()
	default:
		return nil, gerr.Newf(gerr.Syntax, "unknown command %q", kw)
	}
	if err != nil {
		return nil, asSyntax(err)
	}
	if p.cur.Type != TokenEOF {
		return nil, gerr.Newf(gerr.Syntax, "unexpected token %s %q", p.cur.Type, p.cur.Lexeme)
	}
	return stmt, nil
}

func asSyntax(err error) error {
	var ge *gerr.Error
	if errors.As(err, &ge) {
		return err
	}
	return gerr.Wrap(gerr.Syntax, "", err)
}

func (p *Parser) parseCreate() (Stmt, error) {
	kind, err := p.expectKeyword("NODE", "EDGE")
	if err != nil {
		return nil, err
	}
	if kind == "NODE" {
		label, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		props, err := p.optionalProps()
		if err != nil {
			return nil, err
		}
		return CreateNodeStmt{Label: label, Props: props}, nil
	}

	from, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	if err := p.expectType(TokenMinus); err != nil {
		return nil, fmt.Errorf("expected -LABEL->, %w", err)
	}
	label, err := p.expectIdent()
	if err != nil {
		return nil, fmt.Errorf("expected edge label, %w", err)
	}
	if err := p.expectType(TokenArrow); err != nil {
		return nil, fmt.Errorf("expected -> after edge label, %w", err)
	}
	to, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	props, err := p.optionalProps()
	if err != nil {
		return nil, err
	}
	return CreateEdgeStmt{From: from, To: to, Label: label, Props: props}, nil
}

func (p *Parser) parseUpdate() (Stmt, error) {
	kind, err := p.expectKeyword("NODE", "EDGE")
	if err != nil {
		return nil, err
	}
	id, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	if p.cur.Type != TokenLBrace {
		return nil, fmt.Errorf("UPDATE requires a property map")
	}
	props, err := p.parseProps()
	if err != nil {
		return nil, err
	}
	if kind == "NODE" {
		return UpdateNodeStmt{ID: id, Props: props}, nil
	}
	return UpdateEdgeStmt{ID: id, Props: props}, nil
}

func (p *Parser) parseMatch() (Stmt, error) {
	if p.isKeyword("EDGE") {
		p.advance()
		label, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		where, err := p.optionalWhere()
		if err != nil {
			return nil, err
		}
		return MatchEdgeStmt{Label: label, Where: where}, nil
	}
	label, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	where, err := p.optionalWhere()
	if err != nil {
		return nil, err
	}
	return MatchStmt{Label: label, Where: where}, nil
}

func (p *Parser) optionalWhere() (*Where, error) {
	if !p.isKeyword("WHERE") {
		return nil, nil
	}
	p.advance()
	key, err := p.expectIdent()
	if err != nil {
		return nil, err
	}
	op, err := p.expectOp()
	if err != nil {
		return nil, err
	}
	val, err := p.parseValue()
	if err != nil {
		return nil, err
	}
	return &Where{Key: key, Op: op, Value: val}, nil
}

func (p *Parser) parseEdges() (Stmt, error) {
	from, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	if !p.isKeyword("TO") {
		return nil, fmt.Errorf("expected TO")
	}
	p.advance()
	to, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	return EdgesStmt{From: from, To: to}, nil
}

func (p *Parser) parseNeighbors() (Stmt, error) {
	id, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	depth := 1
	if p.isKeyword("DEPTH") {
		p.advance()
		d, err := p.expectUint()
		if err != nil {
			return nil, err
		}
		depth = int(d)
	}
	return NeighborsStmt{ID: id, Depth: depth}, nil
}

func (p *Parser) parsePath() (Stmt, error) {
	from, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	if !p.isKeyword("TO") {
		return nil, fmt.Errorf("expected TO")
	}
	p.advance()
	to, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	return PathStmt{From: from, To: to}, nil
}

func (p *Parser) parseDelete() (Stmt, error) {
	kind, err := p.expectKeyword("NODE", "EDGE")
	if err != nil {
		return nil, err
	}
	id, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	if kind == "NODE" {
		return DeleteNodeStmt{ID: id}, nil
	}
	return DeleteEdgeStmt{ID: id}, nil
}

func (p *Parser) parseGet() (Stmt, error) {
	kind, err := p.expectKeyword("NODE", "EDGE")
	if err != nil {
		return nil, err
	}
	id, err := p.expectUint()
	if err != nil {
		return nil, err
	}
	if kind == "NODE" {
		return GetNodeStmt{ID: id}, nil
	}
	return GetEdgeStmt{ID: id}, nil
}

func (p *Parser) parseShow() (Stmt, error) {
	if !p.isKeyword("STATS") {
		return nil, fmt.Errorf("expected STATS")
	}
	p.advance()
	return ShowStatsStmt{}, nil
}

func (p *Parser) parseSave() (Stmt, error) {
	path, err := p.expectPath()
	if err != nil {
		return nil, err
	}
	return SaveStmt{Path: path}, nil
}

func (p *Parser) parseLoad() (Stmt, error) {
	path, err := p.expectPath()
	if err != nil {
		return nil, err
	}
	return LoadStmt{Path: path}, nil
}

func (p *Parser) expectPath() (string, error) {
	if p.cur.Type == TokenEOF {
		return "", fmt.Errorf("expected file path")
	}
	if p.cur.Type == TokenString {
		s, _ := p.cur.Literal.(string)
		p.advance()
		return s, nil
	}
	path := strings.TrimSpace(p.lx.src[p.cur.Pos:])
	if path == "" {
		return "", fmt.Errorf("expected file path")
	}
	for p.cur.Type != TokenEOF {
		p.advance()
	}
	return path, nil
}

func (p *Parser) optionalProps() (map[string]any, error) {
	if p.cur.Type != TokenLBrace {
		return map[string]any{}, nil
	}
	return p.parseProps()
}

func (p *Parser) parseProps() (map[string]any, error) {
	if err := p.expectType(TokenLBrace); err != nil {
		return nil, err
	}
	props := map[string]any{}
	if p.cur.Type == TokenRBrace {
		p.advance()
		return props, nil
	}
	for {
		key, err := p.expectIdent()
		if err != nil {
			return nil, err
		}
		if err := p.expectType(TokenColon); err != nil {
			return nil, err
		}
		val, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		props[key] = val
		if p.cur.Type == TokenComma {
			p.advance()
			continue
		}
		break
	}
	if err := p.expectType(TokenRBrace); err != nil {
		return nil, err
	}
	return props, nil
}

func (p *Parser) parseValue() (any, error) {
	switch p.cur.Type {
	case TokenString:
		s, _ := p.cur.Literal.(string)
		p.advance()
		return s, nil
	case TokenNumber:
		v := p.cur.Literal
		p.advance()
		return v, nil
	case TokenIdent:
		switch strings.ToLower(p.cur.Lexeme) {
		case "true":
			p.advance()
			return true, nil
		case "false":
			p.advance()
			return false, nil
		default:
			s := p.cur.Lexeme
			p.advance()
			return s, nil
		}
	default:
		return nil, fmt.Errorf("expected value, got %s", p.cur.Type)
	}
}

func (p *Parser) expectOp() (string, error) {
	switch p.cur.Type {
	case TokenEQ:
		p.advance()
		return "=", nil
	case TokenNE:
		p.advance()
		return "!=", nil
	case TokenGT:
		p.advance()
		return ">", nil
	case TokenLT:
		p.advance()
		return "<", nil
	case TokenGE:
		p.advance()
		return ">=", nil
	case TokenLE:
		p.advance()
		return "<=", nil
	default:
		return "", fmt.Errorf("expected comparison operator")
	}
}

func (p *Parser) expectKeyword(opts ...string) (string, error) {
	if p.cur.Type != TokenIdent {
		return "", fmt.Errorf("expected %s", strings.Join(opts, " or "))
	}
	got := strings.ToUpper(p.cur.Lexeme)
	for _, o := range opts {
		if got == o {
			p.advance()
			return got, nil
		}
	}
	return "", fmt.Errorf("expected %s, got %s", strings.Join(opts, " or "), got)
}

func (p *Parser) expectIdent() (string, error) {
	if p.cur.Type != TokenIdent {
		return "", fmt.Errorf("expected identifier, got %s", p.cur.Type)
	}
	s := p.cur.Lexeme
	p.advance()
	return s, nil
}

func (p *Parser) expectUint() (uint64, error) {
	if p.cur.Type != TokenNumber {
		return 0, fmt.Errorf("expected number, got %s", p.cur.Type)
	}
	switch v := p.cur.Literal.(type) {
	case int64:
		if v < 0 {
			return 0, fmt.Errorf("id must be non-negative")
		}
		p.advance()
		return uint64(v), nil
	case float64:
		return 0, fmt.Errorf("expected integer id, got float")
	default:
		n, err := strconv.ParseUint(p.cur.Lexeme, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid number %q", p.cur.Lexeme)
		}
		p.advance()
		return n, nil
	}
}

func (p *Parser) expectType(t TokenType) error {
	if p.cur.Type == TokenError {
		return errOf(p.cur)
	}
	if p.cur.Type != t {
		return fmt.Errorf("expected %s, got %s", t, p.cur.Type)
	}
	p.advance()
	return nil
}

func (p *Parser) isKeyword(kw string) bool {
	return p.cur.Type == TokenIdent && strings.EqualFold(p.cur.Lexeme, kw)
}

func (p *Parser) advance() {
	p.cur = p.lx.Next()
}

func errOf(t Token) error {
	if e, ok := t.Literal.(error); ok {
		return gerr.Wrap(gerr.Syntax, "", e)
	}
	return gerr.Newf(gerr.Syntax, "lex error at %d", t.Pos)
}
