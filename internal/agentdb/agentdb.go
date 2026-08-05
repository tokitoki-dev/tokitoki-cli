// Package agentdb reads the SQLite databases that local AI agents store their
// sessions in.
package agentdb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/tokitoki-dev/tokitoki-cli/internal/agentdata"

	// agentdb is the only package that opens these databases, so the driver
	// registers here.
	_ "modernc.org/sqlite"
)

func floatValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case json.Number:
		parsed, err := strconv.ParseFloat(typed.String(), 64)
		return parsed, err == nil && agentdata.IsFinite(parsed)
	case float64:
		return typed, agentdata.IsFinite(typed)
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil && agentdata.IsFinite(parsed)
	default:
		return 0, false
	}
}

// OpenSQLite opens an agent's database for scanning. Read-only is not a
// preference but a guarantee: these files belong to running agents, and a
// scanner that can take write locks on them can also corrupt them.
func OpenSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func SqlString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case []byte:
		return strings.TrimSpace(string(typed))
	case int64:
		return strconv.FormatInt(typed, 10)
	case float64:
		if !agentdata.IsFinite(typed) {
			return ""
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func SqlUint(value any) uint64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	case int:
		if typed > 0 {
			return uint64(typed)
		}
	case float64:
		return agentdata.FloatToUint(typed)
	case []byte:
		return agentdata.UintValue(string(typed))
	case string:
		return agentdata.UintValue(typed)
	}
	return 0
}

func SqlFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case nil:
		return 0, false
	case float64:
		return typed, !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case int64:
		return float64(typed), true
	case []byte:
		return floatValue(string(typed))
	case string:
		return floatValue(typed)
	default:
		return 0, false
	}
}

func ExistingSQLiteFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
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
	return agentdata.UniqueStrings(dbPaths)
}

func ScanAny(rows *sql.Rows, values ...*any) bool {
	dest := make([]any, len(values))
	for i := range values {
		dest[i] = values[i]
	}
	return rows.Scan(dest...) == nil
}
