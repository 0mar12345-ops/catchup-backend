package services

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// TimetableUploadService parses CSV/XLSX uploads and stores them in MongoDB.
type TimetableUploadService struct {
	timetableCollection   *mongo.Collection
	termOverviewCollection *mongo.Collection
}

func NewTimetableUploadService(client *mongo.Client, dbName string) *TimetableUploadService {
	db := client.Database(dbName)
	return &TimetableUploadService{
		timetableCollection:   db.Collection("timetable_uploads"),
		termOverviewCollection: db.Collection("term_overview_uploads"),
	}
}

func (s *TimetableUploadService) UploadTimetable(ctx context.Context, teacherID, schoolID bson.ObjectID, termLabel string, fileName string, fileBytes []byte) (*models.TimetableUpload, error) {
	entries, err := parseTimetableFile(fileName, fileBytes)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no timetable rows were found in the uploaded file")
	}

	now := time.Now().UTC()
	record := &models.TimetableUpload{
		ID:         bson.NewObjectID(),
		SchoolID:   schoolID,
		TeacherID:  teacherID,
		FileName:   fileName,
		FileType:   strings.ToLower(filepath.Ext(fileName)),
		TermLabel:  strings.TrimSpace(termLabel),
		Entries:    entries,
		UploadedAt: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	_, err = s.timetableCollection.InsertOne(ctx, record)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func (s *TimetableUploadService) UploadTermOverview(ctx context.Context, teacherID, schoolID bson.ObjectID, termLabel string, fileName string, fileBytes []byte) (*models.TermOverviewUpload, error) {
	entries, err := parseTermOverviewFile(fileName, fileBytes)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, errors.New("no term overview rows were found in the uploaded file")
	}

	now := time.Now().UTC()
	record := &models.TermOverviewUpload{
		ID:         bson.NewObjectID(),
		SchoolID:   schoolID,
		TeacherID:  teacherID,
		FileName:   fileName,
		FileType:   strings.ToLower(filepath.Ext(fileName)),
		TermLabel:  strings.TrimSpace(termLabel),
		Entries:    entries,
		UploadedAt: now,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	_, err = s.termOverviewCollection.InsertOne(ctx, record)
	if err != nil {
		return nil, err
	}
	return record, nil
}

func parseTimetableFile(fileName string, data []byte) ([]models.TimetableEntry, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".csv":
		return parseTimetableCSV(data)
	case ".xlsx":
		return parseTimetableXLSX(data)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
}

func parseTermOverviewFile(fileName string, data []byte) ([]models.TermOverviewEntry, error) {
	ext := strings.ToLower(filepath.Ext(fileName))
	switch ext {
	case ".csv":
		return parseTermOverviewCSV(data)
	case ".xlsx":
		return parseTermOverviewXLSX(data)
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}
}

func parseTimetableCSV(data []byte) ([]models.TimetableEntry, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return normalizeTimetableRecords(records)
}

func parseTermOverviewCSV(data []byte) ([]models.TermOverviewEntry, error) {
	reader := csv.NewReader(bytes.NewReader(data))
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return normalizeTermOverviewRecords(records)
}

func normalizeTimetableRecords(records [][]string) ([]models.TimetableEntry, error) {
	if len(records) < 2 {
		return nil, errors.New("timetable file must contain at least one data row")
	}

	headers := normalizeHeaders(records[0])
	entries := make([]models.TimetableEntry, 0)

	for _, row := range records[1:] {
		if allBlank(row) {
			continue
		}
		entry, err := timetableRowToEntry(headers, row)
		if err != nil {
			continue
		}
		if entry.DayNumber < 1 || entry.DayNumber > 5 {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func normalizeTermOverviewRecords(records [][]string) ([]models.TermOverviewEntry, error) {
	if len(records) < 2 {
		return nil, errors.New("term overview file must contain at least one data row")
	}

	headers := normalizeHeaders(records[0])
	entries := make([]models.TermOverviewEntry, 0)

	for _, row := range records[1:] {
		if allBlank(row) {
			continue
		}
		entry, err := termOverviewRowToEntry(headers, row)
		if err != nil {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func normalizeHeaders(row []string) map[string]int {
	m := map[string]int{}
	for i, cell := range row {
		key := strings.ToLower(strings.TrimSpace(cell))
		key = strings.ReplaceAll(key, " ", "_")
		key = strings.ReplaceAll(key, "-", "_")
		key = strings.ReplaceAll(key, "/", "_")
		m[key] = i
	}
	return m
}

func timetableRowToEntry(headers map[string]int, row []string) (models.TimetableEntry, error) {
	dayIdx := valueAt(headers, row, "day", "day_number", "day_no", "weekday")
	periodIdx := valueAt(headers, row, "period", "period_number", "period_no")
	classIdx := valueAt(headers, row, "class", "class_name", "class_name_1")
	roomIdx := valueAt(headers, row, "room", "room_number", "room_no")
	subjectIdx := valueAt(headers, row, "subject", "subject_name")

	day, err := parseInt(cellAt(row, dayIdx))
	if err != nil {
		return models.TimetableEntry{}, err
	}
	period, err := parseInt(cellAt(row, periodIdx))
	if err != nil {
		return models.TimetableEntry{}, err
	}

	return models.TimetableEntry{
		DayNumber:    day,
		PeriodNumber: period,
		ClassName:    cellAt(row, classIdx),
		RoomNumber:   cellAt(row, roomIdx),
		Subject:      cellAt(row, subjectIdx),
	}, nil
}

func termOverviewRowToEntry(headers map[string]int, row []string) (models.TermOverviewEntry, error) {
	weekIdx := valueAt(headers, row, "week", "week_number")
	topicIdx := valueAt(headers, row, "topic", "topic_taught")
	assessmentIdx := valueAt(headers, row, "assessment", "assessment_yes_no", "assessment_status")

	week, err := parseInt(cellAt(row, weekIdx))
	if err != nil {
		return models.TermOverviewEntry{}, err
	}

	assessment := strings.TrimSpace(cellAt(row, assessmentIdx))
	assessmentYN := strings.EqualFold(assessment, "yes") || strings.EqualFold(assessment, "true") || strings.EqualFold(assessment, "y") || assessment == "1"

	return models.TermOverviewEntry{
		WeekNumber:   week,
		TopicTaught:  cellAt(row, topicIdx),
		Assessment:   assessment,
		AssessmentYN: assessmentYN,
	}, nil
}

func valueAt(headers map[string]int, row []string, keys ...string) int {
	for _, key := range keys {
		if idx, ok := headers[key]; ok {
			return idx
		}
	}
	for i, cell := range row {
		candidate := strings.ToLower(strings.TrimSpace(cell))
		candidate = strings.ReplaceAll(candidate, " ", "_")
		candidate = strings.ReplaceAll(candidate, "-", "_")
		candidate = strings.ReplaceAll(candidate, "/", "_")
		for _, key := range keys {
			if candidate == key {
				return i
			}
		}
	}
	return -1
}

func cellAt(row []string, idx int) string {
	if idx < 0 || idx >= len(row) {
		return ""
	}
	return strings.TrimSpace(row[idx])
}

func parseInt(value string) (int, error) {
	if strings.TrimSpace(value) == "" {
		return 0, errors.New("empty numeric value")
	}
	return strconv.Atoi(strings.TrimSpace(value))
}

func allBlank(row []string) bool {
	for _, cell := range row {
		if strings.TrimSpace(cell) != "" {
			return false
		}
	}
	return true
}

// XLSX parsing helpers (minimal workbook reader for .xlsx files).

type xlsxWorkbook struct {
	XMLName xml.Name `xml:"workbook"`
	Sheets  []xlsxSheet `xml:"sheets>sheet"`
}

type xlsxSheet struct {
	Name string `xml:"name,attr"`
	Rid  string `xml:"sheetId,attr"`
}

type xlsxSharedStrings struct {
	XMLName xml.Name `xml:"sst"`
	Items   []xlsxSI `xml:"si"`
}

type xlsxSI struct {
	Text []xlsxT `xml:"t"`
}

type xlsxT struct {
	XMLName xml.Name `xml:"t"`
	Value   string   `xml:",chardata"`
}

type xlsxWorksheet struct {
	XMLName xml.Name `xml:"worksheet"`
	SheetData xlsxSheetData `xml:"sheetData"`
}

type xlsxSheetData struct {
	Rows []xlsxRow `xml:"row"`
}

type xlsxRow struct {
	Cells []xlsxC `xml:"c"`
}

type xlsxC struct {
	R string `xml:"r,attr"`
	T string `xml:"t,attr"`
	V string `xml:"v"`
	IS xlsxIS `xml:"is"`
}

type xlsxIS struct {
	T string `xml:"t"`
}

func parseTimetableXLSX(data []byte) ([]models.TimetableEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	sharedStrings, err := readSharedStrings(zr)
	if err != nil {
		return nil, err
	}
	rows, err := readXLSXRows(zr, sharedStrings)
	if err != nil {
		return nil, err
	}
	return normalizeTimetableRecords(rows)
}

func parseTermOverviewXLSX(data []byte) ([]models.TermOverviewEntry, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	sharedStrings, err := readSharedStrings(zr)
	if err != nil {
		return nil, err
	}
	rows, err := readXLSXRows(zr, sharedStrings)
	if err != nil {
		return nil, err
	}
	return normalizeTermOverviewRecords(rows)
}

func readSharedStrings(zr *zip.Reader) ([]string, error) {
	for _, file := range zr.File {
		if filepath.ToSlash(file.Name) == "xl/sharedStrings.xml" {
			f, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer f.Close()
			body, err := io.ReadAll(f)
			if err != nil {
				return nil, err
			}
			var sst xlsxSharedStrings
			if err := xml.Unmarshal(body, &sst); err != nil {
				return nil, err
			}
			result := make([]string, 0, len(sst.Items))
			for _, item := range sst.Items {
				text := ""
				for _, t := range item.Text {
					text += t.Value
				}
				result = append(result, text)
			}
			return result, nil
		}
	}
	return []string{}, nil
}

func readXLSXRows(zr *zip.Reader, sharedStrings []string) ([][]string, error) {
	for _, file := range zr.File {
		if filepath.ToSlash(file.Name) == "xl/worksheets/sheet1.xml" {
			f, err := file.Open()
			if err != nil {
				return nil, err
			}
			defer f.Close()
			body, err := io.ReadAll(f)
			if err != nil {
				return nil, err
			}
			var ws xlsxWorksheet
			if err := xml.Unmarshal(body, &ws); err != nil {
				return nil, err
			}
			rows := make([][]string, 0, len(ws.SheetData.Rows)+1)
			for _, row := range ws.SheetData.Rows {
				cells := make([]string, 0, len(row.Cells))
				for _, cell := range row.Cells {
					text := cellValue(cell, sharedStrings)
					cells = append(cells, text)
				}
				rows = append(rows, cells)
			}
			return rows, nil
		}
	}
	return nil, errors.New("xlsx workbook does not contain a worksheet")
}

func cellValue(cell xlsxC, sharedStrings []string) string {
	switch cell.T {
	case "s":
		idx, err := strconv.Atoi(strings.TrimSpace(cell.V))
		if err == nil && idx >= 0 && idx < len(sharedStrings) {
			return sharedStrings[idx]
		}
		return ""
	case "inlineStr":
		return cell.IS.T
	default:
		return strings.TrimSpace(cell.V)
	}
}
