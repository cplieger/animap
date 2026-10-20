module github.com/cplieger/animap

go 1.27.3

require (
	github.com/cplieger/animap/mapread v0.0.0-00010101000000-000000000000
	github.com/cplieger/atomicfile/v4 v4.1.0
	github.com/cplieger/httpx/v5 v5.1.0
	github.com/cplieger/jsoncap/v2 v2.0.3
	github.com/cplieger/runesafe/v3 v3.1.0
	github.com/cplieger/slogx v1.7.0
	github.com/cplieger/xmlx v1.0.6
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
)

require golang.org/x/text v0.43.0 // indirect

replace github.com/cplieger/animap/mapread => ./mapread
