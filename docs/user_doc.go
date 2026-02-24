package docs

func userSwaggerPaths() map[string]any {
	return map[string]any{
		"/api/users/oauth/google": map[string]any{
			"get": map[string]any{
				"summary": "Generate Google OAuth URL",
				"responses": map[string]any{
					"200": map[string]any{"description": "OK"},
				},
			},
		},
		"/api/users/oauth/google/callback": map[string]any{
			"get": map[string]any{
				"summary": "Google OAuth callback",
				"parameters": []map[string]any{
					{"name": "state", "in": "query", "required": true, "type": "string"},
					{"name": "code", "in": "query", "required": true, "type": "string"},
				},
				"responses": map[string]any{
					"200": map[string]any{"description": "OK"},
				},
			},
		},
	}
}
