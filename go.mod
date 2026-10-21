module github.com/cplieger/animap

go 1.27.3

require (
	github.com/cplieger/animap/mapread v0.0.0-00010101000000-000000000000
	github.com/cplieger/slogx v1.7.0
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
)

require (
	github.com/cplieger/httpx/v5 v5.1.0 // indirect
	github.com/cplieger/jsoncap/v2 v2.0.3 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/cplieger/animap/mapread => ./mapread
