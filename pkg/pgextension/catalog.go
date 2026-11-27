// Package pgextension defines the native PostgreSQL extension inventory.
package pgextension

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const MaxEntries = 128
const UpgradeUnavailable = "upgrade_extension_unavailable"

const (
	VectorName    = "vector"
	VectorVersion = "0.8.7"
)

var (
	namePattern    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,62}$`)
	versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,62}$`)
)

func Managed(name string) bool {
	return name == "btree_gist" || name == "pg_trgm" || name == VectorName
}

// EnableSQL only admits named extensions from the native image.
func EnableSQL(name string) (string, bool) {
	if name == VectorName {
		return enableVectorSQL, true
	}
	if !Managed(name) {
		return "", false
	}
	return `BEGIN; SET LOCAL search_path = pg_catalog; SET LOCAL lock_timeout = '2s';
		SET LOCAL statement_timeout = '5s'; SET LOCAL ROLE gregale_schema_owner;
		CREATE EXTENSION IF NOT EXISTS "` + name + `" WITH SCHEMA public; COMMIT;`, true
}

// The install script runs in a fresh schema owned by the provider. Moving
// the installed extension to public keeps ordinary pgvector SQL portable.
const enableVectorSQL = `BEGIN;
	SET LOCAL search_path = pg_catalog;
	SET LOCAL lock_timeout = '2s';
	SET LOCAL statement_timeout = '5s';
	SELECT pg_catalog.pg_advisory_xact_lock(72835369179278421);
	CREATE SCHEMA gregale_vector_install;
	REVOKE ALL ON SCHEMA gregale_vector_install FROM PUBLIC;
	CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA gregale_vector_install VERSION '` + VectorVersion + `';
	DO $gregale$
	BEGIN
		IF NOT EXISTS (
			SELECT FROM pg_catalog.pg_extension
			WHERE extname = 'vector' AND extversion = '` + VectorVersion + `'
			AND extowner = current_user::regrole
			AND extnamespace IN ('public'::regnamespace, 'gregale_vector_install'::regnamespace)
		) THEN
			RAISE EXCEPTION 'unexpected managed vector installation';
		END IF;
		ALTER EXTENSION vector SET SCHEMA public;
	END $gregale$;
	DROP SCHEMA gregale_vector_install;
	COMMIT;`

// Only the managed image majors and the pinned version are admitted to the
// privileged install. The guest also verifies the bundled shared library.
const vectorAvailableSQL = `v.name = 'vector'
	AND current_setting('server_version_num')::int / 10000 IN (16, 17, 18)
	AND v.version = '` + VectorVersion + `'`

type Installed struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type Available struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Default bool   `json:"default,omitempty"`
}

type Catalog struct {
	Installed []Installed `json:"installed"`
	Available []Available `json:"available"`
}

func (i Installed) Valid() bool {
	return namePattern.MatchString(i.Name) && versionPattern.MatchString(i.Version)
}

// Valid bounds the catalog at each trust boundary. The SQL reads one extra
// row so a larger catalog fails explicitly instead of losing information.
func (c Catalog) Valid() bool {
	if c.Installed == nil || c.Available == nil || len(c.Installed) > MaxEntries || len(c.Available) > MaxEntries {
		return false
	}
	for _, i := range c.Installed {
		if !i.Valid() {
			return false
		}
	}
	for _, a := range c.Available {
		if !Managed(a.Name) || !versionPattern.MatchString(a.Version) {
			return false
		}
	}
	return true
}

func DecodeInstalled(raw string) ([]Installed, error) {
	var installed []Installed
	if err := json.Unmarshal([]byte(raw), &installed); err != nil || installed == nil || len(installed) > MaxEntries {
		return nil, errors.New("invalid extension inventory")
	}
	for _, i := range installed {
		if !i.Valid() {
			return nil, errors.New("invalid extension inventory")
		}
	}
	return installed, nil
}

const InstalledSQL = `SELECT coalesce(json_agg(e), '[]'::json) FROM (
	SELECT extname AS name, extversion AS version FROM pg_catalog.pg_extension
	WHERE extname <> 'plpgsql' ORDER BY extname LIMIT 129
) e`

const CatalogSQL = `SELECT json_build_object(
	'installed', (` + InstalledSQL + `),
	'available', (SELECT coalesce(json_agg(a), '[]'::json) FROM (
		SELECT v.name, v.version, v.version = e.default_version AS "default"
		FROM pg_catalog.pg_available_extension_versions v
		JOIN pg_catalog.pg_available_extensions e USING (name)
		WHERE (v.trusted AND v.name IN ('btree_gist', 'pg_trgm'))
		   OR (` + vectorAvailableSQL + ` AND v.version = e.default_version)
		ORDER BY v.name, v.version LIMIT 129
	) a))`

// ImportCompatibleSQL reports source versions the target default equals or
// can update from. Vector is installed by the provider before pg_restore.
func ImportCompatibleSQL(source []Installed, skip []string) (string, error) {
	var rows []string
	for _, i := range source {
		if !i.Valid() {
			return "", errors.New("invalid extension inventory")
		}
		if !slices.Contains(skip, i.Name) {
			rows = append(rows, fmt.Sprintf("('%s', '%s')", i.Name, i.Version))
		}
	}
	if len(rows) == 0 {
		return `SELECT '[]'::json`, nil
	}
	return `WITH source(name, version) AS (VALUES ` + strings.Join(rows, ",") + `),
		available AS MATERIALIZED (
			SELECT s.name, s.version, e.default_version
			FROM source s
			JOIN pg_catalog.pg_available_extensions e ON e.name = s.name
			JOIN pg_catalog.pg_available_extension_versions v ON v.name = e.name AND v.version = e.default_version
			WHERE (v.name <> 'vector' AND v.trusted) OR (` + vectorAvailableSQL + `)
		)
		SELECT coalesce(json_agg(json_build_object('name', name, 'version', version) ORDER BY name), '[]'::json)
		FROM available t WHERE t.version = t.default_version OR EXISTS (
			SELECT 1 FROM pg_catalog.pg_extension_update_paths(t.name)
			WHERE source = t.version AND target = t.default_version AND path IS NOT NULL
		)`, nil
}

func ImportBlockers(source, target []Installed, skip []string) []Installed {
	blockers := make([]Installed, 0)
	for _, i := range source {
		if !slices.Contains(skip, i.Name) && !slices.Contains(target, i) {
			blockers = append(blockers, i)
		}
	}
	return blockers
}

// UpgradeBlockers checks that the target image ships the extension control
// file and recognizes the installed version in an install or update script.
// pg_upgrade still checks that the extension's libraries can load.
func UpgradeBlockers(installed []Installed, files []string) []Installed {
	blockers := make([]Installed, 0)
	for _, i := range installed {
		script := i.Name + "--" + i.Version
		if i.Name == VectorName && !slices.Contains(files, "vector.so") ||
			!slices.Contains(files, i.Name+".control") || !slices.ContainsFunc(files, func(file string) bool {
			return file == script+".sql" || strings.HasPrefix(file, script+"--") && strings.HasSuffix(file, ".sql")
		}) {
			blockers = append(blockers, i)
		}
	}
	return blockers
}
