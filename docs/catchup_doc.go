package docs

func catchupSwaggerPaths() map[string]any {
	return map[string]any{
		"/api/catchup/generate": map[string]any{
			"post": map[string]any{
				"summary":     "Generate catch-up lessons for absent students",
				"description": "Creates catch-up lessons for students who were absent on a specific date. Fetches content from Google Classroom, extracts text from attachments (Google Docs, Slides, PDFs), and prepares content for AI generation.",
				"consumes":    []string{"application/json"},
				"produces":    []string{"application/json"},
				"tags":        []string{"Catch-Up"},
				"security": []map[string][]string{
					{"Bearer": {}},
				},
				"parameters": []map[string]any{
					{
						"in":          "body",
						"name":        "body",
						"description": "Catch-up generation request",
						"required":    true,
						"schema": map[string]any{
							"$ref": "#/definitions/GenerateCatchUpRequest",
						},
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Successfully processed catch-up generation",
						"schema": map[string]any{
							"$ref": "#/definitions/GenerateCatchUpResult",
						},
					},
					"400": map[string]any{
						"description": "Bad request - invalid parameters, no content found, or insufficient content",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"401": map[string]any{
						"description": "Unauthorized - authentication required",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"404": map[string]any{
						"description": "Course not found or not accessible",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"500": map[string]any{
						"description": "Internal server error",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		},
	}
}

func catchupSwaggerDefinitions() map[string]any {
	return map[string]any{
		"GenerateCatchUpRequest": map[string]any{
			"type":     "object",
			"required": []string{"course_id", "student_ids", "absence_date"},
			"properties": map[string]any{
				"course_id": map[string]any{
					"type":        "string",
					"description": "Course ObjectID (MongoDB hex string)",
					"example":     "507f1f77bcf86cd799439011",
				},
				"student_ids": map[string]any{
					"type":        "array",
					"description": "Array of student ObjectIDs who were absent",
					"minItems":    1,
					"items": map[string]any{
						"type":    "string",
						"example": "507f191e810c19729de860ea",
					},
				},
				"absence_date": map[string]any{
					"type":        "string",
					"description": "Date of absence in YYYY-MM-DD format",
					"example":     "2026-03-03",
					"pattern":     "^\\d{4}-\\d{2}-\\d{2}$",
				},
			},
		},
		"GenerateCatchUpResult": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"success_count": map[string]any{
					"type":        "integer",
					"description": "Number of students successfully processed",
					"example":     2,
				},
				"failed_count": map[string]any{
					"type":        "integer",
					"description": "Number of students that failed processing",
					"example":     0,
				},
				"warnings": map[string]any{
					"type":        "array",
					"description": "Array of warning messages for issues encountered",
					"items": map[string]any{
						"type":    "string",
						"example": "Failed to extract text from file.pdf: unsupported format",
					},
				},
				"message": map[string]any{
					"type":        "string",
					"description": "Summary message of the operation",
					"example":     "Successfully processed 2 student(s)",
				},
			},
		},
	}
}
