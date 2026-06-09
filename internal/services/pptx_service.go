package services

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/models"
	openai "github.com/sashabaranov/go-openai"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var ErrOpenAINotConfigured = errors.New("OpenAI API key not configured")

type PptxService struct {
	coursesCollection *mongo.Collection
	config            *config.Config
}

func NewPptxService(client *mongo.Client, dbName string, cfg *config.Config) *PptxService {
	return &PptxService{
		coursesCollection: client.Database(dbName).Collection("courses"),
		config:            cfg,
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

// GeneratePptx validates the course, calls OpenAI for slide content, and returns
// a ready-to-download PPTX buffer along with a safe filename.
func (s *PptxService) GeneratePptx(
	ctx context.Context,
	teacherID, schoolID, courseID string,
	weekNumber int,
	dateText, topic string,
) (*bytes.Buffer, string, error) {
	courseOID, err := bson.ObjectIDFromHex(courseID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid course id")
	}
	teacherOID, err := bson.ObjectIDFromHex(teacherID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid teacher id")
	}
	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, "", fmt.Errorf("invalid school id")
	}

	var course models.Course
	if err := s.coursesCollection.FindOne(ctx, bson.M{
		"_id": courseOID, "teacher_id": teacherOID, "school_id": schoolOID,
	}).Decode(&course); err != nil {
		return nil, "", fmt.Errorf("course not found")
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

	content, err := s.generateSlideContent(ctx, course.Name, weekLabel, topicLabel)
	if err != nil {
		return nil, "", err
	}

	buf, err := buildPptx(course.Name, weekLabel, topicLabel, content)
	if err != nil {
		return nil, "", err
	}

	filename := safeFilename(course.Name) + "_" + safeFilename(topicLabel) + ".pptx"
	return buf, filename, nil
}

func (s *PptxService) generateSlideContent(ctx context.Context, courseName, weekLabel, topic string) (*pptxAIContent, error) {
	if s.config.OpenAIAPIKey == "" {
		return nil, ErrOpenAINotConfigured
	}

	client := openai.NewClient(s.config.OpenAIAPIKey)

	prompt := fmt.Sprintf(`You are a teacher creating a structured PowerPoint lesson.
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
// PPTX builder — produces an Open XML presentation using archive/zip.
// All user-supplied strings are XML-escaped before being embedded.
// ---------------------------------------------------------------------------

// pptxSlide represents a single slide to render.
type pptxSlide struct {
	isTitle  bool
	title    string
	subtitle string   // title slide only
	items    []string // content slides
	numbered bool
}

func buildPptx(courseName, weekLabel, topic string, c *pptxAIContent) (*bytes.Buffer, error) {
	slides := assembleSlides(courseName, weekLabel, topic, c)

	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// [Content_Types].xml must be first in the ZIP.
	if err := writeZipEntry(zw, "[Content_Types].xml", contentTypesXML(len(slides))); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "_rels/.rels", rootRelsXML()); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "ppt/presentation.xml", presentationXML(len(slides))); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "ppt/_rels/presentation.xml.rels", presentationRelsXML(len(slides))); err != nil {
		return nil, err
	}
	// Theme is required: the slide master relationship points to it.
	if err := writeZipEntry(zw, "ppt/theme/theme1.xml", themeXML()); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "ppt/slideMasters/slideMaster1.xml", slideMasterXML()); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "ppt/slideMasters/_rels/slideMaster1.xml.rels", slideMasterRelsXML()); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "ppt/slideLayouts/slideLayout1.xml", slideLayoutXML()); err != nil {
		return nil, err
	}
	if err := writeZipEntry(zw, "ppt/slideLayouts/_rels/slideLayout1.xml.rels", slideLayoutRelsXML()); err != nil {
		return nil, err
	}

	for i, s := range slides {
		n := i + 1
		if err := writeZipEntry(zw, fmt.Sprintf("ppt/slides/slide%d.xml", n), renderSlide(s)); err != nil {
			return nil, err
		}
		if err := writeZipEntry(zw, fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", n), slideRelsXML()); err != nil {
			return nil, err
		}
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

func assembleSlides(courseName, weekLabel, topic string, c *pptxAIContent) []pptxSlide {
	slides := []pptxSlide{
		{
			isTitle:  true,
			title:    topic,
			subtitle: courseName + " · " + weekLabel,
		},
	}

	if len(c.LearningIntentions) > 0 {
		slides = append(slides, pptxSlide{title: "Learning Intentions", items: c.LearningIntentions})
	}
	if len(c.SuccessCriteria) > 0 {
		slides = append(slides, pptxSlide{title: "Success Criteria", items: c.SuccessCriteria})
	}
	for _, cs := range c.ContentSlides {
		if cs.Heading != "" && len(cs.Bullets) > 0 {
			slides = append(slides, pptxSlide{title: cs.Heading, items: cs.Bullets})
		}
	}
	if len(c.PracticeQuestions) > 0 {
		slides = append(slides, pptxSlide{title: "Practice Questions", items: c.PracticeQuestions, numbered: true})
	}
	if c.Activity.Heading != "" {
		items := []string{c.Activity.Instructions}
		slides = append(slides, pptxSlide{title: c.Activity.Heading, items: items})
	}
	return slides
}

// ---------------------------------------------------------------------------
// XML generation helpers
// ---------------------------------------------------------------------------

func writeZipEntry(zw *zip.Writer, name, content string) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(content))
	return err
}

// xmlEsc escapes the five XML special characters in a string.
func xmlEsc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func contentTypesXML(numSlides int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/>
  <Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.drawingml.theme+xml"/>
  <Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/>
  <Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/>`)
	for i := 1; i <= numSlides; i++ {
		sb.WriteString("\n  <Override PartName=\"/ppt/slides/slide")
		sb.WriteString(fmt.Sprintf("%d", i))
		sb.WriteString(".xml\" ContentType=\"application/vnd.openxmlformats-officedocument.presentationml.slide+xml\"/>")
	}
	sb.WriteString("\n</Types>")
	return sb.String()
}

func rootRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/>
</Relationships>`
}

func presentationXML(numSlides int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:presentation xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
                xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
                xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
                saveSubsetFonts="1">
  <p:sldMasterIdLst>
    <p:sldMasterId id="2147483648" r:id="rId1"/>
  </p:sldMasterIdLst>
  <p:sldIdLst>`)
	for i := 0; i < numSlides; i++ {
		sb.WriteString(fmt.Sprintf("\n    <p:sldId id=\"%d\" r:id=\"rId%d\"/>", 256+i, 2+i))
	}
	sb.WriteString(`
  </p:sldIdLst>
  <p:sldSz cx="9144000" cy="6858000" type="screen4x3"/>
  <p:notesSz cx="6858000" cy="9144000"/>
</p:presentation>`)
	return sb.String()
}

func presentationRelsXML(numSlides int) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/>`)
	for i := 0; i < numSlides; i++ {
		sb.WriteString(fmt.Sprintf(
			"\n  <Relationship Id=\"rId%d\" Type=\"http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide\" Target=\"slides/slide%d.xml\"/>",
			2+i, i+1,
		))
	}
	sb.WriteString("\n</Relationships>")
	return sb.String()
}

func slideMasterXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldMaster xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
             xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
             xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <p:cSld>
    <p:spTree>
      <p:nvGrpSpPr>
        <p:cNvPr id="1" name=""/>
        <p:cNvGrpSpPr/>
        <p:nvPr/>
      </p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm>
          <a:off x="0" y="0"/>
          <a:ext cx="0" cy="0"/>
          <a:chOff x="0" y="0"/>
          <a:chExt cx="0" cy="0"/>
        </a:xfrm>
      </p:grpSpPr>
    </p:spTree>
  </p:cSld>
  <p:clrMap bg1="lt1" tx1="dk1" bg2="lt2" tx2="dk2"
            accent1="accent1" accent2="accent2" accent3="accent3"
            accent4="accent4" accent5="accent5" accent6="accent6"
            hlink="hlink" folHlink="folHlink"/>
  <p:sldLayoutIdLst>
    <p:sldLayoutId id="2147483649" r:id="rId2"/>
  </p:sldLayoutIdLst>
  <p:txStyles>
    <p:titleStyle><a:lvl1pPr><a:defRPr/></a:lvl1pPr></p:titleStyle>
    <p:bodyStyle><a:lvl1pPr><a:defRPr/></a:lvl1pPr></p:bodyStyle>
    <p:otherStyle><a:lvl1pPr><a:defRPr/></a:lvl1pPr></p:otherStyle>
  </p:txStyles>
</p:sldMaster>`
}

func slideMasterRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
</Relationships>`
}

func slideLayoutXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sldLayout xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
             xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
             xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
             type="blank" preserve="1">
  <p:cSld name="Blank">
    <p:spTree>
      <p:nvGrpSpPr>
        <p:cNvPr id="1" name=""/>
        <p:cNvGrpSpPr/>
        <p:nvPr/>
      </p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm>
          <a:off x="0" y="0"/>
          <a:ext cx="0" cy="0"/>
          <a:chOff x="0" y="0"/>
          <a:chExt cx="0" cy="0"/>
        </a:xfrm>
      </p:grpSpPr>
    </p:spTree>
  </p:cSld>
  <p:clrMapOvr><a:masterClr/></p:clrMapOvr>
</p:sldLayout>`
}

func slideLayoutRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/>
</Relationships>`
}

// slideRelsXML is the same for every slide — each references the single blank layout.
func slideRelsXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/>
</Relationships>`
}

// themeXML returns a minimal but fully valid DrawingML theme.
// PowerPoint requires the slide master to have a theme relationship; without it
// the file is treated as corrupt. The format scheme must contain at least 3 entries
// in each of its four style lists (fillStyleLst, lnStyleLst, effectStyleLst, bgFillStyleLst).
func themeXML() string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="Catchup Theme">
  <a:themeElements>
    <a:clrScheme name="Catchup">
      <a:dk1><a:srgbClr val="1C1917"/></a:dk1>
      <a:lt1><a:srgbClr val="FFFFFF"/></a:lt1>
      <a:dk2><a:srgbClr val="3D3580"/></a:dk2>
      <a:lt2><a:srgbClr val="FAF9F6"/></a:lt2>
      <a:accent1><a:srgbClr val="3D3580"/></a:accent1>
      <a:accent2><a:srgbClr val="5B4FCF"/></a:accent2>
      <a:accent3><a:srgbClr val="C4BBEE"/></a:accent3>
      <a:accent4><a:srgbClr val="16A34A"/></a:accent4>
      <a:accent5><a:srgbClr val="D97706"/></a:accent5>
      <a:accent6><a:srgbClr val="DC2626"/></a:accent6>
      <a:hlink><a:srgbClr val="3D3580"/></a:hlink>
      <a:folHlink><a:srgbClr val="5B4FCF"/></a:folHlink>
    </a:clrScheme>
    <a:fontScheme name="Catchup">
      <a:majorFont>
        <a:latin typeface="Calibri Light"/>
        <a:ea typeface=""/>
        <a:cs typeface=""/>
      </a:majorFont>
      <a:minorFont>
        <a:latin typeface="Calibri"/>
        <a:ea typeface=""/>
        <a:cs typeface=""/>
      </a:minorFont>
    </a:fontScheme>
    <a:fmtScheme name="Catchup">
      <a:fillStyleLst>
        <a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill>
        <a:solidFill><a:srgbClr val="FAF9F6"/></a:solidFill>
        <a:solidFill><a:srgbClr val="3D3580"/></a:solidFill>
      </a:fillStyleLst>
      <a:lnStyleLst>
        <a:ln w="6350" cap="flat" cmpd="sng"><a:solidFill><a:srgbClr val="3D3580"/></a:solidFill><a:prstDash val="solid"/></a:ln>
        <a:ln w="12700" cap="flat" cmpd="sng"><a:solidFill><a:srgbClr val="3D3580"/></a:solidFill><a:prstDash val="solid"/></a:ln>
        <a:ln w="19050" cap="flat" cmpd="sng"><a:solidFill><a:srgbClr val="3D3580"/></a:solidFill><a:prstDash val="solid"/></a:ln>
      </a:lnStyleLst>
      <a:effectStyleLst>
        <a:effectStyle><a:effectLst/></a:effectStyle>
        <a:effectStyle><a:effectLst/></a:effectStyle>
        <a:effectStyle><a:effectLst/></a:effectStyle>
      </a:effectStyleLst>
      <a:bgFillStyleLst>
        <a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill>
        <a:solidFill><a:srgbClr val="FAF9F6"/></a:solidFill>
        <a:solidFill><a:srgbClr val="3D3580"/></a:solidFill>
      </a:bgFillStyleLst>
    </a:fmtScheme>
  </a:themeElements>
</a:theme>`
}

// ---------------------------------------------------------------------------
// Slide renderers — use strings.Builder + xmlEsc to avoid fmt.Sprintf % issues
// with user-provided strings.
// ---------------------------------------------------------------------------

const (
	slideNS = `xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"`

	grpSpTreeHeader = `      <p:nvGrpSpPr>
        <p:cNvPr id="1" name=""/>
        <p:cNvGrpSpPr/>
        <p:nvPr/>
      </p:nvGrpSpPr>
      <p:grpSpPr>
        <a:xfrm>
          <a:off x="0" y="0"/>
          <a:ext cx="0" cy="0"/>
          <a:chOff x="0" y="0"/>
          <a:chExt cx="0" cy="0"/>
        </a:xfrm>
      </p:grpSpPr>`
)

func renderSlide(s pptxSlide) string {
	if s.isTitle {
		return renderTitleSlide(s.title, s.subtitle)
	}
	return renderContentSlide(s.title, s.items, s.numbered)
}

// renderTitleSlide produces a deep-indigo slide with large centred title and a subtitle.
func renderTitleSlide(title, subtitle string) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString("\n<p:sld ")
	sb.WriteString(slideNS)
	sb.WriteString(">\n  <p:cSld>\n    <p:bg><p:bgPr>")
	sb.WriteString(`<a:solidFill><a:srgbClr val="3D3580"/></a:solidFill>`)
	sb.WriteString(`<a:effectLst/></p:bgPr></p:bg>`)
	sb.WriteString("\n    <p:spTree>\n")
	sb.WriteString(grpSpTreeHeader)

	// Title text box — vertically centred on slide
	sb.WriteString("\n      <p:sp>")
	sb.WriteString(`<p:nvSpPr><p:cNvPr id="2" name="Title"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`)
	sb.WriteString(`<p:spPr><a:xfrm><a:off x="457200" y="2000000"/><a:ext cx="8229600" cy="1714500"/></a:xfrm>`)
	sb.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr>`)
	sb.WriteString(`<p:txBody><a:bodyPr wrap="square" anchor="ctr"/><a:lstStyle/>`)
	sb.WriteString(`<a:p><a:pPr algn="ctr"/>`)
	sb.WriteString(`<a:r><a:rPr lang="en-US" sz="4400" b="1" dirty="0">`)
	sb.WriteString(`<a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:rPr>`)
	sb.WriteString("<a:t>")
	sb.WriteString(xmlEsc(title))
	sb.WriteString("</a:t></a:r></a:p></p:txBody></p:sp>")

	// Subtitle text box
	sb.WriteString("\n      <p:sp>")
	sb.WriteString(`<p:nvSpPr><p:cNvPr id="3" name="Subtitle"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`)
	sb.WriteString(`<p:spPr><a:xfrm><a:off x="457200" y="3885900"/><a:ext cx="8229600" cy="685800"/></a:xfrm>`)
	sb.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr>`)
	sb.WriteString(`<p:txBody><a:bodyPr wrap="square" anchor="ctr"/><a:lstStyle/>`)
	sb.WriteString(`<a:p><a:pPr algn="ctr"/>`)
	sb.WriteString(`<a:r><a:rPr lang="en-US" sz="2000" dirty="0">`)
	sb.WriteString(`<a:solidFill><a:srgbClr val="C4BBEE"/></a:solidFill></a:rPr>`)
	sb.WriteString("<a:t>")
	sb.WriteString(xmlEsc(subtitle))
	sb.WriteString("</a:t></a:r></a:p></p:txBody></p:sp>")

	sb.WriteString("\n    </p:spTree>\n  </p:cSld>")
	sb.WriteString("\n  <p:clrMapOvr><a:masterClr/></p:clrMapOvr>")
	sb.WriteString("\n</p:sld>")
	return sb.String()
}

// renderContentSlide produces a slide with an indigo title bar and bullet content below.
func renderContentSlide(title string, items []string, numbered bool) string {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	sb.WriteString("\n<p:sld ")
	sb.WriteString(slideNS)
	sb.WriteString(">\n  <p:cSld>\n    <p:bg><p:bgPr>")
	sb.WriteString(`<a:solidFill><a:srgbClr val="FAF9F6"/></a:solidFill>`)
	sb.WriteString(`<a:effectLst/></p:bgPr></p:bg>`)
	sb.WriteString("\n    <p:spTree>\n")
	sb.WriteString(grpSpTreeHeader)

	// Indigo title bar (background rectangle)
	sb.WriteString("\n      <p:sp>")
	sb.WriteString(`<p:nvSpPr><p:cNvPr id="2" name="TitleBar"/><p:cNvSpPr txBox="0"/><p:nvPr/></p:nvSpPr>`)
	sb.WriteString(`<p:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="9144000" cy="1028700"/></a:xfrm>`)
	sb.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom>`)
	sb.WriteString(`<a:solidFill><a:srgbClr val="3D3580"/></a:solidFill>`)
	sb.WriteString(`<a:ln><a:noFill/></a:ln></p:spPr></p:sp>`)

	// Title text box (overlays the bar)
	sb.WriteString("\n      <p:sp>")
	sb.WriteString(`<p:nvSpPr><p:cNvPr id="3" name="Title"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`)
	sb.WriteString(`<p:spPr><a:xfrm><a:off x="457200" y="171450"/><a:ext cx="8229600" cy="685800"/></a:xfrm>`)
	sb.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr>`)
	sb.WriteString(`<p:txBody><a:bodyPr wrap="square" anchor="ctr"/><a:lstStyle/>`)
	sb.WriteString(`<a:p><a:r><a:rPr lang="en-US" sz="2800" b="1" dirty="0">`)
	sb.WriteString(`<a:solidFill><a:srgbClr val="FFFFFF"/></a:solidFill></a:rPr>`)
	sb.WriteString("<a:t>")
	sb.WriteString(xmlEsc(title))
	sb.WriteString("</a:t></a:r></a:p></p:txBody></p:sp>")

	// Content text box
	sb.WriteString("\n      <p:sp>")
	sb.WriteString(`<p:nvSpPr><p:cNvPr id="4" name="Content"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr>`)
	sb.WriteString(`<p:spPr><a:xfrm><a:off x="457200" y="1143000"/><a:ext cx="8229600" cy="5486400"/></a:xfrm>`)
	sb.WriteString(`<a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr>`)
	sb.WriteString(`<p:txBody><a:bodyPr wrap="square" anchor="t"/><a:lstStyle/>`)

	for i, item := range items {
		sb.WriteString("\n        <a:p>")
		sb.WriteString(`<a:pPr><a:spcBef><a:spcPts val="400"/></a:spcBef></a:pPr>`)
		sb.WriteString(`<a:r><a:rPr lang="en-US" sz="2000" dirty="0">`)
		sb.WriteString(`<a:solidFill><a:srgbClr val="1C1917"/></a:solidFill></a:rPr>`)
		sb.WriteString("<a:t>")
		if numbered {
			sb.WriteString(xmlEsc(fmt.Sprintf("%d.  %s", i+1, item)))
		} else {
			sb.WriteString(xmlEsc("•  " + item))
		}
		sb.WriteString("</a:t></a:r></a:p>")
	}

	sb.WriteString("\n      </p:txBody></p:sp>")
	sb.WriteString("\n    </p:spTree>\n  </p:cSld>")
	sb.WriteString("\n  <p:clrMapOvr><a:masterClr/></p:clrMapOvr>")
	sb.WriteString("\n</p:sld>")
	return sb.String()
}

// ---------------------------------------------------------------------------
// Utilities
// ---------------------------------------------------------------------------

func safeFilename(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			sb.WriteRune(r)
		case r == ' ' || r == '-':
			sb.WriteRune('_')
		}
	}
	result := sb.String()
	if result == "" {
		return "lesson"
	}
	return result
}

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
