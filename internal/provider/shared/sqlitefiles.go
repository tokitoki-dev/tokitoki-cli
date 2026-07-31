package shared

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

func ExistingSQLiteFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
func DecodeJSONObjectString(data string) map[string]any {
	decoder := json.NewDecoder(bytes.NewReader([]byte(data)))
	decoder.UseNumber()
	var record map[string]any
	if err := decoder.Decode(&record); err != nil {
		return nil
	}
	return record
}
func SqliteDBPaths(paths []string, defaultFile string, extraNames func(string) bool) []string {
	dbPaths := make([]string, 0)
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			if filepath.Base(root) == defaultFile || extraNames != nil && extraNames(filepath.Base(root)) {
				dbPaths = append(dbPaths, root)
			}
			continue
		}
		candidate := filepath.Join(root, defaultFile)
		if fileInfo, err := os.Stat(candidate); err == nil && !fileInfo.IsDir() {
			dbPaths = append(dbPaths, candidate)
		}
		if extraNames == nil {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !extraNames(entry.Name()) {
				continue
			}
			dbPaths = append(dbPaths, filepath.Join(root, entry.Name()))
		}
	}
	sort.Strings(dbPaths)
	return UniqueStrings(dbPaths)
}
func ScanAny(rows *sql.Rows, values ...*any) bool {
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = values[i]
	}
	return rows.Scan(dest...) == nil
}
