package datarepair

import (
	"crypto/sha256"
	_ "embed"
	"fmt"
	"regexp"
	"strings"

	"github.com/onebox-faas/faas/pkg/lazyregexp"
)

//go:embed row_contract.sql
var rowContract string

// Exact preceding producer, retained only to qualify reviewed owner upgrades.
//
//go:embed row_contract_v1.sql
var rowContractV1 string

//go:embed row_contract_v2.sql
var rowContractV2 string

//go:embed row_contract_v3.sql
var rowContractV3 string

var functionDefinition = lazyregexp.New(`(?s)CREATE(?: OR REPLACE)? FUNCTION gregale_repair\.([a-z_]+)\((.*?)\) RETURNS ([^\n]+)\nLANGUAGE (.*?) AS \$\$(.*?)\$\$;`)

func embeddedFunction(sql, name string) []string {
	for _, m := range functionDefinition.FindAllStringSubmatch(sql, -1) {
		if m[1] == name {
			return m
		}
	}
	panic("missing embedded repair function: " + name)
}

func sqlLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

// Match complete owner-reviewed metadata, not just a function's name or body.
func rowFunctionMatches(alias string, f []string) string {
	settings := []string{"'search_path=pg_catalog'"}
	for _, setting := range []string{"extra_float_digits=3", "TimeZone=UTC", "DateStyle=ISO, YMD", "IntervalStyle=postgres", "lc_monetary=C"} {
		key, value, _ := strings.Cut(setting, "=")
		if strings.Contains(f[4], "SET "+key+" = '"+value+"'") {
			settings = append(settings, sqlLiteral(setting))
		}
	}
	config := "ARRAY[" + strings.Join(settings, ",") + "]"
	args, result, language, volatility := "''", "'trigger'::regtype", "'plpgsql'", "'v'"
	if f[1] == "fingerprint" {
		args, result, language, volatility = "'regclass'", "'text'::regtype", "'sql'", "'s'"
	}
	return strings.NewReplacer("__FN__", alias).Replace(fmt.Sprintf(`__FN__.pronamespace='gregale_repair'::regnamespace AND __FN__.proname=%s AND
 oidvectortypes(__FN__.proargtypes)=%s AND __FN__.prorettype=%s AND
 __FN__.prolang=(SELECT oid FROM pg_language WHERE lanname=%s) AND __FN__.provolatile=%s AND
 encode(sha256(convert_to(__FN__.prosrc,'UTF8')),'hex')=%s AND __FN__.proconfig=%s AND
 NOT __FN__.prosecdef AND NOT __FN__.proisstrict AND NOT __FN__.proleakproof AND NOT __FN__.proretset AND
 __FN__.prokind='f' AND __FN__.proparallel='u' AND __FN__.prosupport=0 AND __FN__.provariadic=0 AND
 __FN__.proargmodes IS NULL AND __FN__.pronargdefaults=0`, sqlLiteral(f[1]), args, result, language, volatility, sqlLiteral(fmt.Sprintf("%x", sha256.Sum256([]byte(f[5])))), config))
}

func currentRowContractSQL() string {
	current := schemaLimits().Replace(rowContract)
	checks := make([]string, 0, 2)
	for _, name := range []string{"guard_row", "capture_row"} {
		checks = append(checks, "(SELECT count(*)=1 FROM pg_proc capture_fn WHERE "+rowFunctionMatches("capture_fn", embeddedFunction(current, name))+")")
	}
	return strings.Join(checks, " AND ")
}

// The stricter trigger versions preserve the legacy fingerprint contribution
// only for these exact bodies and catalog attributes. Arbitrary replacements
// never acquire that compatibility, and capture requires the current versions.
func rowContractSchema() string {
	return monetaryRowContract(renderingRowContract(timezoneRowContract(rowFingerprintSchema(rowContract, true))))
}

func monetaryRowContract(previous string) string {
	f := embeddedFunction(previous, "fingerprint")[0]
	updated := strings.Replace(f, "SET IntervalStyle = 'postgres' AS", "SET IntervalStyle = 'postgres' SET lc_monetary = 'C' AS", 1)
	updated = strings.Replace(updated, "SELECT set_config('DateStyle',", "SELECT set_config('lc_monetary',coalesce((SELECT fingerprint_monetary FROM gregale_repair.enrollments WHERE relation=target),'C'),true),set_config('DateStyle',", 1)
	return strings.Replace(previous, f, updated, 1)
}

func previousV3RowContractSchema() string {
	return renderingRowContract(timezoneRowContract(rowFingerprintSchema(rowContractV3, true)))
}

func renderingRowContract(previous string) string {
	f := embeddedFunction(previous, "fingerprint")[0]
	updated := strings.Replace(f, "SET TimeZone = 'UTC' AS", "SET TimeZone = 'UTC' SET DateStyle = 'ISO, YMD' SET IntervalStyle = 'postgres' AS", 1)
	updated = strings.Replace(updated, "SELECT set_config('TimeZone',", "SELECT set_config('DateStyle',coalesce((SELECT fingerprint_datestyle FROM gregale_repair.enrollments WHERE relation=target),'ISO, YMD'),true),set_config('IntervalStyle',coalesce((SELECT fingerprint_intervalstyle FROM gregale_repair.enrollments WHERE relation=target),'postgres'),true),set_config('TimeZone',", 1)
	return strings.Replace(previous, f, updated, 1)
}

// Exact preceding installation, including its canonical trigger contributions.
func previousV2RowContractSchema() string {
	return timezoneRowContract(rowFingerprintSchema(rowContractV2, true))
}

func timezoneRowContract(previous string) string {
	fingerprint := embeddedFunction(previous, "fingerprint")[0]
	// The function's TimeZone setting restores the caller's setting on return.
	// Probes inline this catalog-only query in their own rolled-back snapshot.
	fingerprint = strings.Replace(fingerprint, "SET search_path = pg_catalog AS", "SET search_path = pg_catalog SET TimeZone = 'UTC' AS", 1)
	fingerprint = strings.Replace(fingerprint, " SELECT encode", ` WITH fingerprint_zone AS MATERIALIZED (
 SELECT set_config('TimeZone',coalesce((SELECT fingerprint_timezone FROM gregale_repair.enrollments WHERE relation=target),'UTC'),true)
 )
 SELECT encode`, 1)
	fingerprint = strings.Replace(fingerprint, "$$;", " FROM fingerprint_zone\n$$;", 1)
	return strings.Replace(previous, embeddedFunction(previous, "fingerprint")[0], fingerprint, 1)
}

// Retain this exact reviewed definition for upgrades from the preceding repair
// contract. Its output also proves a legacy enrollment's rendering timezone.
func previousRowContractSchema() string {
	return rowFingerprintSchema(rowContractV1, false)
}

func rowFingerprintSchema(rows string, includePrior bool) string {
	current := schemaLimits().Replace(rows)
	legacy := schemaLimits().Replace(schema)
	versions := []string{current}
	if includePrior {
		if rows == rowContract {
			versions = append(versions, schemaLimits().Replace(rowContractV3))
		}
		if rows == rowContract || rows == rowContractV3 {
			versions = append(versions, schemaLimits().Replace(rowContractV2))
		}
		versions = append(versions, schemaLimits().Replace(rowContractV1))
	}
	canonical := "CASE"
	for _, name := range []string{"guard_row", "capture_row"} {
		seen := map[string]bool{}
		for _, version := range versions {
			match := rowFunctionMatches("p", embeddedFunction(version, name))
			if !seen[match] {
				canonical += " WHEN " + match + " THEN " + sqlLiteral(embeddedFunction(legacy, name)[5])
				seen[match] = true
			}
		}
	}
	canonical += " ELSE p.prosrc END"
	fingerprint := embeddedFunction(legacy, "fingerprint")[0]
	fingerprint = strings.Replace(fingerprint, "CREATE FUNCTION", "CREATE OR REPLACE FUNCTION", 1)
	fingerprint = strings.Replace(fingerprint, "p.prosrc)", canonical+")", 1)
	return fingerprint + "\n" + current
}

func upgradeRowContract() string {
	current, legacy := rowContractSchema(), schemaLimits().Replace(schema)
	previous := previousRowContractSchema()
	versions := []string{legacy, previous, timezoneRowContract(previous), previousV2RowContractSchema(), previousV3RowContractSchema(), current}
	checks := make([]string, 0, 3)
	for _, name := range []string{"guard_row", "capture_row", "fingerprint"} {
		matches := make([]string, 0, len(versions))
		for _, version := range versions {
			matches = append(matches, rowFunctionMatches("upgrade_fn", embeddedFunction(version, name)))
		}
		checks = append(checks, "(SELECT count(*)=1 FROM pg_proc upgrade_fn WHERE ("+strings.Join(matches, " OR ")+"))")
	}
	proof := embeddedFunction(rowFingerprintSchema(rowContract, true), "fingerprint")[5]
	proof = regexp.MustCompile(`\btarget\b`).ReplaceAllString(proof, "e.relation")
	renderProof := embeddedFunction(timezoneRowContract(rowFingerprintSchema(rowContract, true)), "fingerprint")[5]
	renderProof = regexp.MustCompile(`\btarget\b`).ReplaceAllString(renderProof, "e.relation")
	monetaryProof := embeddedFunction(renderingRowContract(timezoneRowContract(rowFingerprintSchema(rowContract, true))), "fingerprint")[5]
	monetaryProof = regexp.MustCompile(`\btarget\b`).ReplaceAllString(monetaryProof, "e.relation")
	return "DO $$ BEGIN IF NOT (" + strings.Join(checks, " AND ") + ") THEN RAISE EXCEPTION 'repair_row_contract_upgrade_requires_review' USING ERRCODE='22023'; END IF; END $$;\n" + fingerprintTimezoneColumn + fingerprintRenderingColumns + fingerprintMonetaryColumn + `
DO $$ DECLARE e gregale_repair.enrollments; actual text;
BEGIN
 FOR e IN SELECT * FROM gregale_repair.enrollments WHERE fingerprint_timezone IS NULL LOOP
  EXECUTE format('LOCK TABLE %s IN ACCESS SHARE MODE',e.relation);
  SELECT (` + proof + `) INTO actual;
  IF actual=e.fingerprint THEN
   UPDATE gregale_repair.enrollments SET fingerprint_timezone=current_setting('TimeZone') WHERE relation=e.relation;
  END IF;
 END LOOP;
END $$;
DO $$ DECLARE e gregale_repair.enrollments; actual text;
 caller_zone text:=current_setting('TimeZone'); caller_date text:=current_setting('DateStyle'); caller_interval text:=current_setting('IntervalStyle');
BEGIN
 FOR e IN SELECT * FROM gregale_repair.enrollments WHERE fingerprint_timezone IS NOT NULL AND (fingerprint_datestyle IS NULL OR fingerprint_intervalstyle IS NULL) LOOP
  EXECUTE format('LOCK TABLE %s IN ACCESS SHARE MODE',e.relation);
  PERFORM set_config('DateStyle',coalesce(e.fingerprint_datestyle,caller_date),true),set_config('IntervalStyle',coalesce(e.fingerprint_intervalstyle,caller_interval),true);
  SELECT (` + renderProof + `) INTO actual;
  IF actual=e.fingerprint THEN
   UPDATE gregale_repair.enrollments SET fingerprint_datestyle=coalesce(e.fingerprint_datestyle,caller_date),fingerprint_intervalstyle=coalesce(e.fingerprint_intervalstyle,caller_interval) WHERE relation=e.relation;
  END IF;
 END LOOP;
 PERFORM set_config('TimeZone',caller_zone,true),set_config('DateStyle',caller_date,true),set_config('IntervalStyle',caller_interval,true);
END $$;
DO $$ DECLARE e gregale_repair.enrollments; actual text;
 caller_zone text:=current_setting('TimeZone'); caller_date text:=current_setting('DateStyle'); caller_interval text:=current_setting('IntervalStyle');
BEGIN
 FOR e IN SELECT * FROM gregale_repair.enrollments WHERE fingerprint_monetary IS NULL LOOP
  EXECUTE format('LOCK TABLE %s IN ACCESS SHARE MODE',e.relation);
  PERFORM set_config('TimeZone',coalesce(e.fingerprint_timezone,caller_zone),true),
   set_config('DateStyle',coalesce(e.fingerprint_datestyle,caller_date),true),set_config('IntervalStyle',coalesce(e.fingerprint_intervalstyle,caller_interval),true);
  SELECT (` + monetaryProof + `) INTO actual;
  IF actual=e.fingerprint THEN
   UPDATE gregale_repair.enrollments SET fingerprint_monetary=current_setting('lc_monetary') WHERE relation=e.relation;
  END IF;
 END LOOP;
 PERFORM set_config('TimeZone',caller_zone,true),set_config('DateStyle',caller_date,true),set_config('IntervalStyle',caller_interval,true);
END $$;
` + current
}

const fingerprintTimezoneColumn = `ALTER TABLE gregale_repair.enrollments ADD COLUMN IF NOT EXISTS fingerprint_timezone text CHECK (octet_length(fingerprint_timezone) BETWEEN 1 AND 256);`

const fingerprintRenderingColumns = `
ALTER TABLE gregale_repair.enrollments ADD COLUMN IF NOT EXISTS fingerprint_datestyle text CHECK (octet_length(fingerprint_datestyle) BETWEEN 1 AND 256);
ALTER TABLE gregale_repair.enrollments ADD COLUMN IF NOT EXISTS fingerprint_intervalstyle text CHECK (fingerprint_intervalstyle IN ('postgres','postgres_verbose','sql_standard','iso_8601'));
`

const fingerprintMonetaryColumn = `ALTER TABLE gregale_repair.enrollments ADD COLUMN IF NOT EXISTS fingerprint_monetary text CHECK (octet_length(fingerprint_monetary) BETWEEN 1 AND 256);`
