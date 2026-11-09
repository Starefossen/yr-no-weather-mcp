module github.com/starefossen/yr-no-weather-mcp

go 1.26.10

require github.com/mark3labs/mcp-go v1.1.1

require (
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3 // indirect
	github.com/spf13/cast v1.10.0 // indirect
	github.com/starefossen/home/libs/mcpauth v0.0.0
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/text v0.42.0 // indirect
)

replace github.com/starefossen/home/libs/mcpauth => ../../libs/mcpauth
