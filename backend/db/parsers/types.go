package parsers

type FieldSeed struct {
	Name  string
	Label string
	Type  string
}

type ParserSeed struct {
	Name        string
	Description string
	DeviceType  string
	MatchType   string
	MatchValue  string
	Regex       string
	Fields      []FieldSeed
}