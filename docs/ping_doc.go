package docs

func pingSwaggerPaths() map[string]any {
	return map[string]any{
		"/api/ping": map[string]any{
			"get": map[string]any{
				"summary": "Ping",
				"responses": map[string]any{
					"200": map[string]any{"description": "OK"},
				},
			},
		},
	}
}
