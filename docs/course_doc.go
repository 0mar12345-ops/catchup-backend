package docs

func courseSwaggerPaths() map[string]any {
	return map[string]any{
		"/api/courses": map[string]any{
			"get": map[string]any{
				"summary": "List dashboard courses",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OK",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"courses": map[string]any{
									"type":  "array",
									"items": map[string]any{"$ref": "#/definitions/CourseDashboardItem"},
								},
							},
						},
					},
					"401": map[string]any{"description": "Unauthorized"},
				},
			},
		},
	}
}

func courseSwaggerDefinitions() map[string]any {
	return map[string]any{
		"CourseDashboardItem": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":             map[string]any{"type": "string"},
				"name":           map[string]any{"type": "string"},
				"section":        map[string]any{"type": "string"},
				"subject":        map[string]any{"type": "string"},
				"grade_level":    map[string]any{"type": "string"},
				"source":         map[string]any{"type": "string"},
				"is_archived":    map[string]any{"type": "boolean"},
				"total_students": map[string]any{"type": "integer"},
			},
		},
	}
}
