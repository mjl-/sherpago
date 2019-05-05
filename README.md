# Sherpago

Sherpago reads the (machine-readable) documentation for a [sherpa API](https://www.ueber.net/who/mjl/sherpa/) as generated by sherpadoc, and outputs a documented Go package with a client with all functions and types from the sherpa documentation.  Example:

	# Fetch the API description as sherpadoc.
	# The author of the API probably used "sherpadoc MyAPI" to generate this documentation.
	curl https://example.org/myapi/_docs >myapi.json

	# Turn the sherpadoc into a Go client library.
	sherpago MyAPI https://example.org/myapi/ < myapi.json > myapi.go
	gofmt -w myapi.go

Read the [sherpago documentation at godoc.org/github.com/mjl-/sherpago](https://godoc.org/github.com/mjl-/sherpago).

# Info

Written by Mechiel Lukkien, mechiel@ueber.net, feedback welcome.
MIT-licensed, contains 3-clause BSD licensed code from github.com/golang/lint.

# TODO

- check if identifiers (type names, function names) are keywords in go. if so, rename them so they are not, and don't clash with existing names.

- write tests, both for library and generated code

- think about adding helper for dealing with errors. eg whether it is a sherpa, server or user error.
- either return error message or use another name when we get duplicate identifiers (type or field or function names) after turning a name from sherpadoc into a proper Go identifier. currently we generate Go code that won't compile.
- reformat comments, turning markdown from sherpadoc into more readable Go comments. e.g. turn bullet lists into indented wrapped text.
