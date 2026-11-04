package datarepair

import "strings"

// JSON journals cannot retain SQL array bounds or plain-json duplicate keys.
// Follow domain and composite members without executing application functions.
func lossyValueTypesSQL(relation, fields string) string {
	return valueTypesSQL(relation, fields, "value_type.oid='json'::regtype OR value_type.typcategory='A'")
}

func unprovenCaptureSQL(operation string) string {
	fields := "ARRAY(SELECT attname FROM pg_attribute WHERE attrelid=e.relation AND attnum>0 AND NOT attisdropped)"
	risky := valueTypesSQL("e.relation", fields, "value_type.oid IN ('json'::regtype,'jsonb'::regtype,'numeric'::regtype,'float4'::regtype,'float8'::regtype) OR value_type.typcategory='A'")
	unprovenNative := "(" + risky + " AND NOT EXISTS(SELECT 1 FROM gregale_repair.operations capture_proof WHERE capture_proof.id=" + operation + " AND capture_proof.capture_contract_version IN (1,2,3)))"
	ambiguousTypes := valueTypesSQL("e.relation", "e.fields||e.keys", "value_type.oid IN ('float4'::regtype,'float8'::regtype,'jsonb'::regtype)")
	unprovenImages := `NOT EXISTS(SELECT 1 FROM gregale_repair.operations capture_proof WHERE capture_proof.id=` + operation + ` AND capture_proof.capture_contract_version IN (2,3)) AND (
 (EXISTS(SELECT 1 FROM gregale_repair.operations capture_proof WHERE capture_proof.id=` + operation + ` AND capture_proof.payload_purged_at IS NOT NULL) AND ` + ambiguousTypes + `) OR EXISTS(
 WITH RECURSIVE image_values(type_oid,value) AS (
  SELECT a.atttypid,image.value FROM gregale_repair.changes image_change CROSS JOIN LATERAL (
   SELECT key,value FROM jsonb_each(image_change.before_values)
   UNION ALL SELECT key,value FROM jsonb_each(image_change.after_values)
   UNION ALL SELECT key,value FROM jsonb_each(image_change.key)
  ) image JOIN pg_attribute a ON a.attrelid=e.relation AND a.attname=image.key AND a.attnum>0 AND NOT a.attisdropped
  WHERE image_change.operation_id=` + operation + `
  UNION
  SELECT nested.type_oid,nested.value FROM image_values v JOIN pg_type value_type ON value_type.oid=v.type_oid
  CROSS JOIN LATERAL (
   SELECT value_type.typbasetype AS type_oid,v.value WHERE value_type.typtype='d'
   UNION ALL SELECT a.atttypid,v.value->a.attname FROM pg_attribute a WHERE a.attrelid=value_type.typrelid AND a.attnum>0 AND NOT a.attisdropped
  ) nested WHERE nested.type_oid<>0
 )
 SELECT 1 FROM image_values v WHERE
 (v.type_oid IN ('float4'::regtype,'float8'::regtype) AND v.value='0'::jsonb) OR
 (v.type_oid='jsonb'::regtype AND v.value='null'::jsonb)))`
	intervals := valueTypesSQL("e.relation", "e.fields||e.keys", "value_type.oid='interval'::regtype")
	unprovenIntervals := "(" + intervals + " AND NOT EXISTS(SELECT 1 FROM gregale_repair.operations interval_proof WHERE interval_proof.id=" + operation + " AND interval_proof.capture_contract_version=3))"
	return "(" + unprovenIntervals + " OR " + unprovenTypeSQL(operation) + " OR " + unprovenNative + " OR (" + unprovenImages + "))"
}

func unprovenPlanSQL() string {
	// Earlier inverse selection used JSONB equality, which omits numeric scale
	// changes. Only original candidates can grant authority in a replacement.
	scaleFields := valueTypesSQL("e.relation", "e.fields", "value_type.oid IN ('numeric'::regtype,'jsonb'::regtype)")
	return "(" + unprovenCaptureSQL("p.source_operation_id") + ` OR (
 p.document->>'value_contract_version' IS DISTINCT FROM '2' AND ` + scaleFields + ` AND
 EXISTS(SELECT 1 FROM gregale_repair.operations original WHERE original.id=p.source_operation_id AND original.payload_purged_at IS NOT NULL)
 ) OR EXISTS(
 SELECT 1 FROM gregale_repair.changes source_change
 JOIN LATERAL jsonb_array_elements(p.document->'items') candidate ON candidate->>'row_identity'=source_change.row_identity::text AND candidate->>'status'='candidate'
 CROSS JOIN LATERAL jsonb_each(source_change.before_values) original(field,value)
 WHERE p.document->>'value_contract_version' IS DISTINCT FROM '2' AND
 source_change.operation_id=p.source_operation_id AND original.value=source_change.after_values->original.field AND
 original.value::text IS DISTINCT FROM (source_change.after_values->original.field)::text AND
 (candidate->'before'->original.field)::text IS DISTINCT FROM original.value::text))`
}

func valueTypesSQL(relation, fields, predicate string) string {
	return strings.NewReplacer("__RELATION__", relation, "__FIELDS__", fields, "__PREDICATE__", predicate).Replace(`EXISTS(
 WITH RECURSIVE value_types(oid) AS (
  SELECT a.atttypid FROM pg_attribute a WHERE a.attrelid=__RELATION__ AND a.attnum>0 AND NOT a.attisdropped AND a.attname=ANY(__FIELDS__)
  UNION
  SELECT nested.oid FROM value_types v JOIN pg_type value_type ON value_type.oid=v.oid
  CROSS JOIN LATERAL (
   SELECT value_type.typbasetype AS oid WHERE value_type.typtype='d'
   UNION SELECT a.atttypid FROM pg_attribute a WHERE a.attrelid=value_type.typrelid AND a.attnum>0 AND NOT a.attisdropped
   UNION SELECT value_type.typelem WHERE value_type.typelem<>0
   UNION SELECT value_range.rngsubtype FROM pg_range value_range WHERE value_range.rngtypid=value_type.oid OR value_range.rngmultitypid=value_type.oid
   UNION SELECT value_range.rngtypid FROM pg_range value_range WHERE value_range.rngmultitypid=value_type.oid
  ) nested WHERE nested.oid<>0
 )
 SELECT 1 FROM value_types v JOIN pg_type value_type ON value_type.oid=v.oid
 WHERE __PREDICATE__
)`)
}
