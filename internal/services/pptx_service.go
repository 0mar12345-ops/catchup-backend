package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/models"
	openai "github.com/sashabaranov/go-openai"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	ErrOpenAINotConfigured = errors.New("OpenAI API key not configured")
	ErrGoogleOAuthRequired = errors.New("google account not connected – please re-authorise in Settings")
)

type PptxService struct {
	coursesCollection *mongo.Collection
	oauthCollection   *mongo.Collection
	config            *config.Config
	oauthConfig       *oauth2.Config
}

func NewPptxService(client *mongo.Client, dbName string, cfg *config.Config) *PptxService {
	db := client.Database(dbName)
	return &PptxService{
		coursesCollection: db.Collection("courses"),
		oauthCollection:   db.Collection("oauth_credentials"),
		config:            cfg,
		oauthConfig: &oauth2.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			Endpoint:     google.Endpoint,
			// Scopes here are used only for token refresh; the actual granted scopes
			// come from the stored refresh token issued during initial OAuth consent.
			Scopes: []string{"https://www.googleapis.com/auth/presentations"},
		},
	}
}

// pptxAIContent is the structured JSON response from OpenAI.
type pptxAIContent struct {
	LearningIntentions []string `json:"learning_intentions"`
	SuccessCriteria    []string `json:"success_criteria"`
	ContentSlides      []struct {
		Heading string   `json:"heading"`
		Bullets []string `json:"bullets"`
	} `json:"content_slides"`
	PracticeQuestions []string `json:"practice_questions"`
	Activity          struct {
		Heading      string `json:"heading"`
		Instructions string `json:"instructions"`
	} `json:"activity"`
}

// GeneratePptx creates a Google Slides presentation and returns its edit URL.
func (s *PptxService) GeneratePptx(
	ctx context.Context,
	teacherID, schoolID, courseID string,
	weekNumber int,
	dateText, topic string,
) (string, error) {
	courseOID, err := bson.ObjectIDFromHex(courseID)
	if err != nil {
		return "", fmt.Errorf("invalid course id")
	}
	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		return "", fmt.Errorf("invalid teacher id")
	}
	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return "", fmt.Errorf("invalid school id")
	}

	var course models.Course
	if err := s.coursesCollection.FindOne(ctx, bson.M{
		"_id": courseOID, "teacher_id": teacherOID, "school_id": schoolOID,
	}).Decode(&course); err != nil {
		return "", fmt.Errorf("course not found")
	}

	weekLabel := fmt.Sprintf("Week %d", weekNumber)
	if weekNumber <= 0 {
		weekLabel = "Current week"
	}
	if strings.TrimSpace(dateText) != "" {
		if parsed, err := time.Parse("2006-01-02", dateText); err == nil {
			weekLabel = parsed.Format("02 Jan 2006")
		}
	}

	topicLabel := strings.TrimSpace(topic)
	if topicLabel == "" {
		topicLabel = "Class recap"
	}

	httpClient, err := s.getOAuthClient(ctx, teacherOID, schoolOID)
	if err != nil {
		return "", err
	}

	content, err := s.generateSlideContent(ctx, course.Name, weekLabel, topicLabel)
	if err != nil {
		return "", err
	}

	return s.createGoogleSlidesPresentation(ctx, httpClient, course.Name, weekLabel, topicLabel, content)
}

// getOAuthClient retrieves the teacher's stored token, auto-refreshes it, and
// returns an authorised http.Client for the Google Slides API.
func (s *PptxService) getOAuthClient(ctx context.Context, teacherOID, schoolOID bson.ObjectID) (*http.Client, error) {
	var cred models.OAuthCredential
	err := s.oauthCollection.FindOne(ctx, bson.M{
		"user_id":   teacherOID,
		"school_id": schoolOID,
	}).Decode(&cred)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrGoogleOAuthRequired
		}
		return nil, err
	}
	if cred.RefreshTokenEnc == "" {
		return nil, ErrGoogleOAuthRequired
	}

	token := &oauth2.Token{
		AccessToken:  cred.AccessTokenEnc,
		RefreshToken: cred.RefreshTokenEnc,
		TokenType:    "Bearer",
	}
	if cred.AccessTokenExpiry != nil {
		token.Expiry = *cred.AccessTokenExpiry
	}

	freshToken, err := s.oauthConfig.TokenSource(ctx, token).Token()
	if err != nil {
		s.oauthCollection.UpdateOne(ctx, bson.M{"_id": cred.ID}, bson.M{"$set": bson.M{ //nolint:errcheck
			"status": "invalid", "updated_at": time.Now().UTC(),
		}})
		return nil, ErrGoogleOAuthRequired
	}

	if freshToken.AccessToken != cred.AccessTokenEnc {
		s.oauthCollection.UpdateOne(ctx, bson.M{"_id": cred.ID}, bson.M{"$set": bson.M{ //nolint:errcheck
			"access_token_enc":    freshToken.AccessToken,
			"access_token_expiry": freshToken.Expiry,
			"refresh_token_enc":   freshToken.RefreshToken,
			"status":              "valid",
			"updated_at":          time.Now().UTC(),
		}})
	}

	return s.oauthConfig.Client(ctx, freshToken), nil
}

// ---------------------------------------------------------------------------
// OpenAI content generation
// ---------------------------------------------------------------------------

func (s *PptxService) generateSlideContent(ctx context.Context, courseName, weekLabel, topic string) (*pptxAIContent, error) {
	if s.config.OpenAIAPIKey == "" {
		return nil, ErrOpenAINotConfigured
	}

	client := openai.NewClient(s.config.OpenAIAPIKey)

	prompt := fmt.Sprintf(`You are a teacher creating a structured lesson presentation.
Course: %s
Week: %s
Topic: %s

Return ONLY a raw JSON object (no markdown, no code fences) with this exact structure:
{
  "learning_intentions": ["3-4 concise learning intention statements"],
  "success_criteria": ["4-6 success criteria statements (each starts with 'I can...')"],
  "content_slides": [
    {"heading": "slide heading", "bullets": ["3-5 concise bullet points"]},
    {"heading": "slide heading", "bullets": ["3-5 concise bullet points"]},
    {"heading": "slide heading", "bullets": ["3-5 concise bullet points"]}
  ],
  "practice_questions": ["4-5 practice questions"],
  "activity": {"heading": "Activity title", "instructions": "2-3 sentence activity instructions"}
}

Use plain text only. Keep all text concise and classroom-ready.`,
		courseName, weekLabel, topic)

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: "You are a helpful assistant that returns only valid JSON with no markdown formatting."},
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
		MaxTokens:   2000,
		Temperature: 0.7,
	})
	if err != nil {
		return nil, fmt.Errorf("OpenAI error: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response from OpenAI")
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	raw = stripMarkdownFences(raw)

	var content pptxAIContent
	if err := json.Unmarshal([]byte(raw), &content); err != nil {
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}
	return &content, nil
}

// ---------------------------------------------------------------------------
// Google Slides REST API
// ---------------------------------------------------------------------------

const slidesAPIBase = "https://slides.googleapis.com/v1/presentations"

// slidesM is a convenience alias for the JSON objects the Slides API accepts.
type slidesM = map[string]interface{}

// createGoogleSlidesPresentation creates a new presentation, populates it with
// all slides via a single batchUpdate, and returns the edit URL.
func (s *PptxService) createGoogleSlidesPresentation(
	ctx context.Context,
	httpClient *http.Client,
	courseName, weekLabel, topic string,
	c *pptxAIContent,
) (string, error) {
	// Step 1 — create blank presentation.
	createResp, err := slidesPost(ctx, httpClient, slidesAPIBase, slidesM{
		"title": topic + " – " + courseName + " · " + weekLabel,
	})
	if err != nil {
		return "", fmt.Errorf("failed to create presentation: %w", err)
	}

	presentationID, _ := createResp["presentationId"].(string)
	if presentationID == "" {
		return "", fmt.Errorf("unexpected response from Google Slides API")
	}

	// Capture the default slide Google creates so we can remove it.
	defaultSlideID := ""
	if rawSlides, ok := createResp["slides"].([]interface{}); ok && len(rawSlides) > 0 {
		if s0, ok := rawSlides[0].(map[string]interface{}); ok {
			defaultSlideID, _ = s0["objectId"].(string)
		}
	}

	// Step 2 — build all batch-update requests.
	var (
		reqs []slidesM
		idx  int
	)
	add := func(r slidesM) { reqs = append(reqs, r) }

	if defaultSlideID != "" {
		add(slidesM{"deleteObject": slidesM{"objectId": defaultSlideID}})
	}

	// ── inline helpers ──────────────────────────────────────────────────────

	mkSlide := func() string {
		id := fmt.Sprintf("cslide_%d", idx)
		add(slidesM{
			"createSlide": slidesM{
				"objectId":             id,
				"insertionIndex":       idx,
				"slideLayoutReference": slidesM{"predefinedLayout": "BLANK"},
			},
		})
		idx++
		return id
	}

	setBg := func(sid, hex string) {
		add(slidesM{
			"updatePageProperties": slidesM{
				"objectId": sid,
				"pageProperties": slidesM{
					"pageBackgroundFill": slidesM{
						"solidFill": slidesM{"color": slidesRGB(hex)},
					},
				},
				"fields": "pageBackgroundFill",
			},
		})
	}

	mkTextBox := func(id, sid string, x, y, w, h float64) {
		add(slidesM{
			"createShape": slidesM{
				"objectId":  id,
				"shapeType": "TEXT_BOX",
				"elementProperties": slidesM{
					"pageObjectId": sid,
					"size":         slidesPT(w, h),
					"transform":    slidesXY(x, y),
				},
			},
		})
	}

	mkRect := func(id, sid string, x, y, w, h float64) {
		add(slidesM{
			"createShape": slidesM{
				"objectId":  id,
				"shapeType": "RECTANGLE",
				"elementProperties": slidesM{
					"pageObjectId": sid,
					"size":         slidesPT(w, h),
					"transform":    slidesXY(x, y),
				},
			},
		})
	}

	putText := func(id, text string) {
		add(slidesM{
			"insertText": slidesM{
				"objectId":       id,
				"insertionIndex": 0,
				"text":           text,
			},
		})
	}

	styleText := func(id string, sizePT float64, bold bool, hex string) {
		style := slidesM{
			"fontSize":        slidesM{"magnitude": sizePT, "unit": "PT"},
			"foregroundColor": slidesM{"opaqueColor": slidesRGB(hex)},
		}
		fields := "fontSize,foregroundColor"
		if bold {
			style["bold"] = true
			fields += ",bold"
		}
		add(slidesM{
			"updateTextStyle": slidesM{
				"objectId":  id,
				"textRange": slidesM{"type": "ALL"},
				"style":     style,
				"fields":    fields,
			},
		})
	}

	centerPara := func(id string) {
		add(slidesM{
			"updateParagraphStyle": slidesM{
				"objectId":  id,
				"textRange": slidesM{"type": "ALL"},
				"style":     slidesM{"alignment": "CENTER"},
				"fields":    "alignment",
			},
		})
	}

	noDecor := func(id string) {
		add(slidesM{
			"updateShapeProperties": slidesM{
				"objectId": id,
				"shapeProperties": slidesM{
					"shapeBackgroundFill": slidesM{"propertyState": "NOT_RENDERED"},
					"outline":             slidesM{"propertyState": "NOT_RENDERED"},
				},
				"fields": "shapeBackgroundFill,outline",
			},
		})
	}

	solidRect := func(id, hex string) {
		add(slidesM{
			"updateShapeProperties": slidesM{
				"objectId": id,
				"shapeProperties": slidesM{
					"shapeBackgroundFill": slidesM{
						"solidFill": slidesM{"color": slidesRGB(hex)},
					},
					"outline": slidesM{"propertyState": "NOT_RENDERED"},
				},
				"fields": "shapeBackgroundFill,outline",
			},
		})
	}

	// ── Title slide (deep-indigo background) ────────────────────────────────
	sid := mkSlide()
	setBg(sid, "3D3580")

	tID := sid + "_t"
	mkTextBox(tID, sid, 40, 130, 640, 200)
	putText(tID, topic)
	styleText(tID, 40, true, "FFFFFF")
	centerPara(tID)
	noDecor(tID)

	stID := sid + "_s"
	mkTextBox(stID, sid, 40, 355, 640, 80)
	putText(stID, courseName+" · "+weekLabel)
	styleText(stID, 20, false, "C4BBEE")
	centerPara(stID)
	noDecor(stID)

	// ── Content slide builder ────────────────────────────────────────────────
	addContent := func(title string, items []string, numbered bool) {
		sid := mkSlide()
		setBg(sid, "FAF9F6")

		barID := sid + "_bar"
		mkRect(barID, sid, 0, 0, 720, 72)
		solidRect(barID, "3D3580")

		titleID := sid + "_t"
		mkTextBox(titleID, sid, 28, 14, 664, 50)
		putText(titleID, title)
		styleText(titleID, 22, true, "FFFFFF")
		noDecor(titleID)

		contentID := sid + "_c"
		mkTextBox(contentID, sid, 28, 82, 664, 435)

		var lines []string
		for i, item := range items {
			if numbered {
				lines = append(lines, fmt.Sprintf("%d.  %s", i+1, item))
			} else {
				lines = append(lines, "•  "+item)
			}
		}
		putText(contentID, strings.Join(lines, "\n"))
		styleText(contentID, 18, false, "1C1917")
		add(slidesM{
			"updateParagraphStyle": slidesM{
				"objectId":  contentID,
				"textRange": slidesM{"type": "ALL"},
				"style": slidesM{
					"lineSpacing": 140.0,
					"spaceAbove":  slidesM{"magnitude": 6.0, "unit": "PT"},
				},
				"fields": "lineSpacing,spaceAbove",
			},
		})
		noDecor(contentID)
	}

	if len(c.LearningIntentions) > 0 {
		addContent("Learning Intentions", c.LearningIntentions, false)
	}
	if len(c.SuccessCriteria) > 0 {
		addContent("Success Criteria", c.SuccessCriteria, false)
	}
	for _, cs := range c.ContentSlides {
		if cs.Heading != "" && len(cs.Bullets) > 0 {
			addContent(cs.Heading, cs.Bullets, false)
		}
	}
	if len(c.PracticeQuestions) > 0 {
		addContent("Practice Questions", c.PracticeQuestions, true)
	}
	if c.Activity.Heading != "" {
		addContent(c.Activity.Heading, []string{c.Activity.Instructions}, false)
	}

	// Step 3 — send the batch update.
	batchURL := slidesAPIBase + "/" + presentationID + ":batchUpdate"
	if _, err := slidesPost(ctx, httpClient, batchURL, slidesM{"requests": reqs}); err != nil {
		return "", fmt.Errorf("failed to build slides: %w", err)
	}

	return "https://docs.google.com/presentation/d/" + presentationID + "/edit", nil
}

// ---------------------------------------------------------------------------
// Google Slides HTTP helpers
// ---------------------------------------------------------------------------

func slidesPost(ctx context.Context, client *http.Client, url string, body slidesM) (slidesM, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error.Message != "" {
			if errResp.Error.Status == "PERMISSION_DENIED" || resp.StatusCode == 403 {
				return nil, ErrGoogleOAuthRequired
			}
			return nil, fmt.Errorf("Google Slides API: %s", errResp.Error.Message)
		}
		return nil, fmt.Errorf("Google Slides API HTTP %d", resp.StatusCode)
	}

	var result slidesM
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// slidesRGB converts a 6-hex-digit color to the Google Slides color object
// used in solidFill.color and OptionalColor.opaqueColor.
func slidesRGB(hex string) slidesM {
	if len(hex) != 6 {
		return slidesM{"rgbColor": slidesM{"red": 0.0, "green": 0.0, "blue": 0.0}}
	}
	var r, g, b int
	fmt.Sscanf(hex[0:2], "%x", &r)
	fmt.Sscanf(hex[2:4], "%x", &g)
	fmt.Sscanf(hex[4:6], "%x", &b)
	return slidesM{
		"rgbColor": slidesM{
			"red":   float64(r) / 255.0,
			"green": float64(g) / 255.0,
			"blue":  float64(b) / 255.0,
		},
	}
}

// slidesPT returns a Slides API Size object in points.
func slidesPT(w, h float64) slidesM {
	return slidesM{
		"width":  slidesM{"magnitude": w, "unit": "PT"},
		"height": slidesM{"magnitude": h, "unit": "PT"},
	}
}

// slidesXY returns a Slides API AffineTransform for a translation (position).
func slidesXY(x, y float64) slidesM {
	return slidesM{
		"scaleX":     1.0,
		"scaleY":     1.0,
		"translateX": x,
		"translateY": y,
		"unit":       "PT",
	}
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func stripMarkdownFences(s string) string {
	if idx := strings.Index(s, "```json"); idx != -1 {
		s = s[idx+7:]
	} else if idx := strings.Index(s, "```"); idx != -1 {
		s = s[idx+3:]
	}
	if idx := strings.LastIndex(s, "```"); idx != -1 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}
