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
		"/api/catchup/course/{courseId}/student/{studentId}": map[string]any{
			"get": map[string]any{
				"summary":     "Get catch-up lesson for review",
				"description": "Retrieves a catch-up lesson with all details for teacher review before delivery to student",
				"produces":    []string{"application/json"},
				"tags":        []string{"Catch-Up"},
				"security": []map[string][]string{
					{"Bearer": {}},
				},
				"parameters": []map[string]any{
					{
						"in":          "path",
						"name":        "courseId",
						"description": "Course ObjectID",
						"required":    true,
						"type":        "string",
					},
					{
						"in":          "path",
						"name":        "studentId",
						"description": "Student ObjectID",
						"required":    true,
						"type":        "string",
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Catch-up lesson details",
						"schema": map[string]any{
							"$ref": "#/definitions/CatchUpLessonReview",
						},
					},
					"401": map[string]any{
						"description": "Unauthorized",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"403": map[string]any{
						"description": "Forbidden - access denied",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"404": map[string]any{
						"description": "Catch-up lesson not found",
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
		"/api/catchup/lesson/{lessonId}/deliver": map[string]any{
			"post": map[string]any{
				"summary":     "Deliver catch-up lesson to student",
				"description": "Marks a catch-up lesson as delivered, making it available to the student",
				"produces":    []string{"application/json"},
				"tags":        []string{"Catch-Up"},
				"security": []map[string][]string{
					{"Bearer": {}},
				},
				"parameters": []map[string]any{
					{
						"in":          "path",
						"name":        "lessonId",
						"description": "Catch-up lesson ObjectID",
						"required":    true,
						"type":        "string",
					},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "Lesson delivered successfully",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"message": map[string]any{"type": "string"},
							},
						},
					},
					"401": map[string]any{
						"description": "Unauthorized",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"403": map[string]any{
						"description": "Forbidden - access denied",
						"schema": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"error": map[string]any{"type": "string"},
							},
						},
					},
					"404": map[string]any{
						"description": "Catch-up lesson not found",
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
		"CatchUpLessonReview": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"lesson": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"_id": map[string]any{
							"type": "string",
						},
						"courseId": map[string]any{
							"type": "string",
						},
						"studentId": map[string]any{
							"type": "string",
						},
						"absentDate": map[string]any{
							"type":   "string",
							"format": "date-time",
						},
						"explanation": map[string]any{
							"type": "string",
						},
						"quiz": map[string]any{
							"type": "array",
							"items": map[string]any{
								"$ref": "#/definitions/QuizQuestion",
							},
						},
						"status": map[string]any{
							"type": "string",
							"enum": []string{"empty", "generated", "delivered", "completed"},
						},
						"createdAt": map[string]any{
							"type":   "string",
							"format": "date-time",
						},
						"updatedAt": map[string]any{
							"type":   "string",
							"format": "date-time",
						},
					},
				},
				"student": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"_id": map[string]any{
							"type": "string",
						},
						"name": map[string]any{
							"type": "string",
						},
						"email": map[string]any{
							"type": "string",
						},
					},
				},
				"course": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"_id": map[string]any{
							"type": "string",
						},
						"name": map[string]any{
							"type": "string",
						},
					},
				},
				"contentAudit": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"totalItems": map[string]any{
							"type": "integer",
						},
						"includedItems": map[string]any{
							"type": "array",
							"items": map[string]any{
								"$ref": "#/definitions/ContentItem",
							},
						},
						"excludedItems": map[string]any{
							"type": "array",
							"items": map[string]any{
								"$ref": "#/definitions/ContentItem",
							},
						},
					},
				},
				"warnings": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
				},
			},
		},
		"QuizQuestion": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"question": map[string]any{
					"type": "string",
				},
				"options": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string",
					},
				},
				"correctAnswer": map[string]any{
					"type": "integer",
				},
				"explanation": map[string]any{
					"type": "string",
				},
			},
		},
		"ContentItem": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"title": map[string]any{
					"type": "string",
				},
				"type": map[string]any{
					"type": "string",
				},
				"wordCount": map[string]any{
					"type": "integer",
				},
				"extractedText": map[string]any{
					"type": "string",
				},
				"excluded": map[string]any{
					"type": "boolean",
				},
				"reason": map[string]any{
					"type": "string",
				},
			},
		},
	}
}
