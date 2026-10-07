package lexer

import (
	"fmt"
	"strings"
)

// Kind is a lexical token class. Keywords are their own kinds.
// A keyword after '.' or ':' stays a keyword; the parser accepts it as a name there.
type Kind int

// Token kinds. KwAdmin through KwYield are the reserved words, in order.
const (
	EOF Kind = iota
	Ident
	Int
	Float
	String
	Param

	LParen
	RParen
	LBracket
	RBracket
	LBrace
	RBrace
	Comma
	Colon
	Dot
	Semi
	Plus
	Minus
	Star
	Slash
	Percent
	Caret
	Eq
	NotEq
	Lt
	Gt
	LtEq
	GtEq
	Pipe
	Dollar
	DotDot
	ColonColon
	Arrow
	LeftArrow
	PlusEq

	KwAdmin
	KwAll
	KwAnalyze
	KwAnd
	KwAny
	KwAs
	KwAsc
	KwBegin
	KwBy
	KwCall
	KwCase
	KwChange
	KwCommit
	KwConfirm
	KwConstraint
	KwConstraints
	KwContains
	KwCreate
	KwDatabase
	KwDatabases
	KwDelete
	KwDeny
	KwDesc
	KwDetach
	KwDistinct
	KwDrop
	KwEdge
	KwElse
	KwEnd
	KwEnds
	KwExpires
	KwExplain
	KwFalse
	KwFirst
	KwFor
	KwFrom
	KwGrant
	KwIn
	KwIndex
	KwIndexes
	KwInf
	KwIs
	KwKeys
	KwLabels
	KwLast
	KwLimit
	KwMatch
	KwMerge
	KwNan
	KwNone
	KwNot
	KwNull
	KwNulls
	KwOn
	KwOptional
	KwOr
	KwOrder
	KwPassword
	KwPrivileges
	KwProfile
	KwProperty
	KwRead
	KwReduce
	KwRemove
	KwRequired
	KwReturn
	KwRevoke
	KwRole
	KwRoles
	KwRollback
	KwSet
	KwShow
	KwSingle
	KwSkip
	KwStarts
	KwStats
	KwTerminate
	KwThen
	KwTo
	KwToken
	KwTransaction
	KwTransactions
	KwTrue
	KwTypes
	KwUnion
	KwUnique
	KwUnwind
	KwUse
	KwUser
	KwUsers
	KwVacuum
	KwWhen
	KwWhere
	KwWith
	KwWrite
	KwXor
	KwYield
)

// Token is one lexical token. Line and Col are 1-based. Col is a byte offset
// in the line. Offset is the byte index of the token in the source.
// Text is the decoded identifier, parameter name, string, keyword spelling,
// operator spelling, or numeric lexeme. Int and Float are set for those kinds.
type Token struct {
	Kind   Kind
	Text   string
	Int    int64
	Float  float64
	Line   int
	Col    int
	Offset int
}

// At returns the 1-based position as "line:col".
func (t Token) At() string {
	return fmt.Sprintf("%d:%d", t.Line, t.Col)
}

// String returns the kind's display spelling.
func (k Kind) String() string {
	if s, ok := kindName[k]; ok {
		return s
	}
	return fmt.Sprintf("kind(%d)", int(k))
}

// Keyword reports whether k is a reserved word.
func (k Kind) Keyword() bool {
	return k >= KwAdmin && k <= KwYield
}

// IsKeyword reports whether s is a reserved word, ignoring case.
func IsKeyword(s string) bool {
	_, ok := keywords[strings.ToUpper(s)]
	return ok
}

var kindName = map[Kind]string{
	EOF:        "EOF",
	Ident:      "ident",
	Int:        "int",
	Float:      "float",
	String:     "string",
	Param:      "param",
	LParen:     "(",
	RParen:     ")",
	LBracket:   "[",
	RBracket:   "]",
	LBrace:     "{",
	RBrace:     "}",
	Comma:      ",",
	Colon:      ":",
	Dot:        ".",
	Semi:       ";",
	Plus:       "+",
	Minus:      "-",
	Star:       "*",
	Slash:      "/",
	Percent:    "%",
	Caret:      "^",
	Eq:         "=",
	NotEq:      "!=",
	Lt:         "<",
	Gt:         ">",
	LtEq:       "<=",
	GtEq:       ">=",
	Pipe:       "|",
	Dollar:     "$",
	DotDot:     "..",
	ColonColon: "::",
	Arrow:      "->",
	LeftArrow:  "<-",
	PlusEq:     "+=",

	KwAdmin:        "ADMIN",
	KwAll:          "ALL",
	KwAnalyze:      "ANALYZE",
	KwAnd:          "AND",
	KwAny:          "ANY",
	KwAs:           "AS",
	KwAsc:          "ASC",
	KwBegin:        "BEGIN",
	KwBy:           "BY",
	KwCall:         "CALL",
	KwCase:         "CASE",
	KwChange:       "CHANGE",
	KwCommit:       "COMMIT",
	KwConfirm:      "CONFIRM",
	KwConstraint:   "CONSTRAINT",
	KwConstraints:  "CONSTRAINTS",
	KwContains:     "CONTAINS",
	KwCreate:       "CREATE",
	KwDatabase:     "DATABASE",
	KwDatabases:    "DATABASES",
	KwDelete:       "DELETE",
	KwDeny:         "DENY",
	KwDesc:         "DESC",
	KwDetach:       "DETACH",
	KwDistinct:     "DISTINCT",
	KwDrop:         "DROP",
	KwEdge:         "EDGE",
	KwElse:         "ELSE",
	KwEnd:          "END",
	KwEnds:         "ENDS",
	KwExpires:      "EXPIRES",
	KwExplain:      "EXPLAIN",
	KwFalse:        "FALSE",
	KwFirst:        "FIRST",
	KwFor:          "FOR",
	KwFrom:         "FROM",
	KwGrant:        "GRANT",
	KwIn:           "IN",
	KwIndex:        "INDEX",
	KwIndexes:      "INDEXES",
	KwInf:          "INF",
	KwIs:           "IS",
	KwKeys:         "KEYS",
	KwLabels:       "LABELS",
	KwLast:         "LAST",
	KwLimit:        "LIMIT",
	KwMatch:        "MATCH",
	KwMerge:        "MERGE",
	KwNan:          "NAN",
	KwNone:         "NONE",
	KwNot:          "NOT",
	KwNull:         "NULL",
	KwNulls:        "NULLS",
	KwOn:           "ON",
	KwOptional:     "OPTIONAL",
	KwOr:           "OR",
	KwOrder:        "ORDER",
	KwPassword:     "PASSWORD",
	KwPrivileges:   "PRIVILEGES",
	KwProfile:      "PROFILE",
	KwProperty:     "PROPERTY",
	KwRead:         "READ",
	KwReduce:       "REDUCE",
	KwRemove:       "REMOVE",
	KwRequired:     "REQUIRED",
	KwReturn:       "RETURN",
	KwRevoke:       "REVOKE",
	KwRole:         "ROLE",
	KwRoles:        "ROLES",
	KwRollback:     "ROLLBACK",
	KwSet:          "SET",
	KwShow:         "SHOW",
	KwSingle:       "SINGLE",
	KwSkip:         "SKIP",
	KwStarts:       "STARTS",
	KwStats:        "STATS",
	KwTerminate:    "TERMINATE",
	KwThen:         "THEN",
	KwTo:           "TO",
	KwToken:        "TOKEN",
	KwTransaction:  "TRANSACTION",
	KwTransactions: "TRANSACTIONS",
	KwTrue:         "TRUE",
	KwTypes:        "TYPES",
	KwUnion:        "UNION",
	KwUnique:       "UNIQUE",
	KwUnwind:       "UNWIND",
	KwUse:          "USE",
	KwUser:         "USER",
	KwUsers:        "USERS",
	KwVacuum:       "VACUUM",
	KwWhen:         "WHEN",
	KwWhere:        "WHERE",
	KwWith:         "WITH",
	KwWrite:        "WRITE",
	KwXor:          "XOR",
	KwYield:        "YIELD",
}

var keywords map[string]Kind

func init() {
	keywords = make(map[string]Kind, KwYield-KwAdmin+1)
	for k := KwAdmin; k <= KwYield; k++ {
		name, ok := kindName[k]
		if !ok {
			panic("lexer: keyword kind has no name")
		}
		keywords[name] = k
	}
}
