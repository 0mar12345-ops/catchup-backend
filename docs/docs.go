package docs

import (
	"encoding/json"

	"github.com/swaggo/swag"
)

var SwaggerInfo = &swag.Spec{
	Version:          "1.0",
	Host:             "localhost:8080",
	BasePath:         "/",
	Schemes:          []string{"http", "https"},
	Title:            "GClass AI API",
	Description:      "Pilot MVP API documentation",
	InfoInstanceName: "swagger",
	SwaggerTemplate:  "{}",
	LeftDelim:        "{{",
	RightDelim:       "}}",
}

func init() {
	template, err := buildSwaggerTemplate()
	if err != nil {
		panic(err)
	}

	SwaggerInfo.SwaggerTemplate = template
	swag.Register(SwaggerInfo.InstanceName(), SwaggerInfo)
}

func buildSwaggerTemplate() (string, error) {
	doc := baseSwaggerDoc()

	mergePaths(doc, pingSwaggerPaths())
	mergePaths(doc, schoolSwaggerPaths())
	mergePaths(doc, userSwaggerPaths())
	mergePaths(doc, courseSwaggerPaths())
	mergePaths(doc, catchupSwaggerPaths())

	mergeDefinitions(doc, schoolSwaggerDefinitions())
	mergeDefinitions(doc, courseSwaggerDefinitions())
	mergeDefinitions(doc, catchupSwaggerDefinitions())

	bytes, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}

	return string(bytes), nil
}

func baseSwaggerDoc() map[string]any {
	return map[string]any{
		"swagger": "2.0",
		"info": map[string]any{
			"title":       "GClass AI API",
			"description": "Pilot MVP API documentation",
			"version":     "1.0",
		},
		"basePath":    "/",
		"schemes":     []string{"http", "https"},
		"paths":       map[string]any{},
		"definitions": map[string]any{},
	}
}

func mergePaths(doc map[string]any, paths map[string]any) {
	target := doc["paths"].(map[string]any)
	for k, v := range paths {
		target[k] = v
	}
}

func mergeDefinitions(doc map[string]any, definitions map[string]any) {
	target := doc["definitions"].(map[string]any)
	for k, v := range definitions {
		target[k] = v
	}
}
