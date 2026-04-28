package docs

func schoolSwaggerPaths() map[string]any {
	return map[string]any{
		"/api/schools": map[string]any{
			"get": map[string]any{
				"summary": "List schools",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OK",
						"schema": map[string]any{
							"type":  "array",
							"items": map[string]any{"$ref": "#/definitions/School"},
						},
					},
				},
			},
			"post": map[string]any{
				"summary": "Create school",
				"parameters": []map[string]any{
					{
						"in":       "body",
						"name":     "body",
						"required": true,
						"schema":   map[string]any{"$ref": "#/definitions/CreateSchoolRequest"},
					},
				},
				"responses": map[string]any{
					"201": map[string]any{
						"description": "Created",
						"schema":      map[string]any{"$ref": "#/definitions/School"},
					},
				},
			},
		},
		"/api/schools/{id}": map[string]any{
			"get": map[string]any{
				"summary": "Get school",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "type": "string"},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OK",
						"schema":      map[string]any{"$ref": "#/definitions/School"},
					},
				},
			},
			"put": map[string]any{
				"summary": "Update school",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "type": "string"},
					{
						"in":       "body",
						"name":     "body",
						"required": true,
						"schema":   map[string]any{"$ref": "#/definitions/UpdateSchoolRequest"},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OK",
						"schema":      map[string]any{"$ref": "#/definitions/School"},
					},
				},
			},
			"delete": map[string]any{
				"summary": "Delete school",
				"parameters": []map[string]any{
					{"name": "id", "in": "path", "required": true, "type": "string"},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "OK"},
				},
			},
		},
	}
}

func schoolSwaggerDefinitions() map[string]any {
	return map[string]any{
		"School": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":         map[string]any{"type": "string"},
				"name":       map[string]any{"type": "string"},
				"code":       map[string]any{"type": "string"},
				"domain":     map[string]any{"type": "string"},
				"timezone":   map[string]any{"type": "string"},
				"is_active":  map[string]any{"type": "boolean"},
				"created_at": map[string]any{"type": "string", "format": "date-time"},
				"updated_at": map[string]any{"type": "string", "format": "date-time"},
			},
		},
		"CreateSchoolRequest": map[string]any{
			"type":     "object",
			"required": []string{"name", "code"},
			"properties": map[string]any{
				"name":      map[string]any{"type": "string"},
				"code":      map[string]any{"type": "string"},
				"domain":    map[string]any{"type": "string"},
				"timezone":  map[string]any{"type": "string"},
				"is_active": map[string]any{"type": "boolean"},
			},
		},
		"UpdateSchoolRequest": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":      map[string]any{"type": "string"},
				"code":      map[string]any{"type": "string"},
				"domain":    map[string]any{"type": "string"},
				"timezone":  map[string]any{"type": "string"},
				"is_active": map[string]any{"type": "boolean"},
			},
		},
	}
}
