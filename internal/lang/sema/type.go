package sema

// Type is a static kind. Any means the checker does not know a single kind yet.
type Type int

// Static kinds. Graph values stay Any until a function or a binding says otherwise.
const (
	Any Type = iota
	Null
	Bool
	Int
	Float
	Number
	String
	Bytes
	Date
	DateTime
	Duration
	List
	Map
	Node
	Edge
	Path
	Entity
)

func (t Type) String() string {
	switch t {
	case Any:
		return "any"
	case Null:
		return "null"
	case Bool:
		return "bool"
	case Int:
		return "int"
	case Float:
		return "float"
	case Number:
		return "number"
	case String:
		return "string"
	case Bytes:
		return "bytes"
	case Date:
		return "date"
	case DateTime:
		return "datetime"
	case Duration:
		return "duration"
	case List:
		return "list"
	case Map:
		return "map"
	case Node:
		return "node"
	case Edge:
		return "edge"
	case Path:
		return "path"
	case Entity:
		return "node or edge"
	default:
		return "value"
	}
}

func compatible(got, want Type) bool {
	if got == Any || want == Any || got == Null || want == Null {
		return true
	}
	if got == want {
		return true
	}
	if want == Number && (got == Int || got == Float || got == Number) {
		return true
	}
	if got == Number && (want == Int || want == Float) {
		return true
	}
	if want == Entity && (got == Node || got == Edge || got == Entity) {
		return true
	}
	if got == Entity && (want == Node || want == Edge) {
		return true
	}
	return false
}

func numeric(t Type) bool {
	return t == Int || t == Float || t == Number
}
