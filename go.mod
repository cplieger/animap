module github.com/cplieger/animap

go 1.27.3

require (
	github.com/cplieger/animap/mapread v0.0.0-00010101000000-000000000000
	github.com/cplieger/go/slogx v1.0.0-dev.3
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
)

require (
	github.com/cplieger/go/httpclient v1.0.0-dev.6 // indirect
	github.com/cplieger/go/jsonx v1.0.0-dev.3 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace github.com/cplieger/animap/mapread => ./mapread
