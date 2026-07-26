package parsers

type FieldSeed struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	Type  string `json:"type"`
}

type ParserSeed struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	DeviceType  string      `json:"device_type"`
	MatchType   string      `json:"match_type"`
	MatchValue  string      `json:"match_value"`
	Regex       string      `json:"regex"`
	Fields      []FieldSeed `json:"fields"`
}
