package agentusage

import (
	"database/sql"
	"fmt"
	"math"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

func openSQLite(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
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

func sqlString(value any) string {
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
		if !isFinite(typed) {
			return ""
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func sqlUint(value any) uint64 {
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
		return floatToUint(typed)
	case []byte:
		return uintValue(string(typed))
	case string:
		return uintValue(typed)
	}
	return 0
}

func sqlFloat(value any) (float64, bool) {
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
