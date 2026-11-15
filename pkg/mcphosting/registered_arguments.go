package mcphosting

import (
	"errors"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

type registeredSchemaLoader struct{}

func (registeredSchemaLoader) Load(string) (any, error) {
	return nil, errors.New("registered tool schemas cannot load external references")
}

func validateRegisteredArguments(schema map[string]any, arguments map[string]any) error {
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(registeredSchemaLoader{})
	const name = "urn:gregale:registered-tool-input"
	if err := compiler.AddResource(name, schema); err != nil {
		return err
	}
	compiled, err := compiler.Compile(name)
	if err != nil {
		return err
	}
	if err = compiled.Validate(arguments); err != nil {
		return err
	}
	_, err = parameterHeaders(schema, arguments)
	return err
}
