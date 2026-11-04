package datarepair

import "strings"

func cascadingFieldsSQL(relation, fields string) string {
	return `EXISTS(SELECT 1 FROM pg_constraint cascade_constraint CROSS JOIN LATERAL unnest(cascade_constraint.confkey) k
 JOIN pg_attribute a ON a.attrelid=cascade_constraint.confrelid AND a.attnum=k
 WHERE cascade_constraint.confrelid=` + relation + ` AND cascade_constraint.contype='f' AND cascade_constraint.confupdtype IN ('c','n','d') AND a.attname=ANY((` + fields + `)||ARRAY['gregale_row_version']))`
}

// An index can execute support functions without a pg_depend edge to them.
// Inspect its classes and the same-type family members used for maintenance.
func unsafeIndexClassesSQL(relation string) string {
	return `EXISTS(WITH RECURSIVE index_types(oid) AS (
 SELECT index_attribute.atttypid FROM pg_index selected_index JOIN pg_attribute index_attribute ON index_attribute.attrelid=selected_index.indexrelid
 WHERE selected_index.indrelid=` + relation + ` AND index_attribute.attnum BETWEEN 1 AND selected_index.indnkeyatts AND NOT index_attribute.attisdropped
 UNION
 SELECT nested.oid FROM index_types visited JOIN pg_type index_type ON index_type.oid=visited.oid CROSS JOIN LATERAL (
  SELECT index_type.typbasetype AS oid WHERE index_type.typtype='d'
  UNION SELECT index_type.typelem WHERE index_type.typelem<>0
  UNION SELECT member_attribute.atttypid FROM pg_attribute member_attribute WHERE member_attribute.attrelid=index_type.typrelid AND member_attribute.attnum>0 AND NOT member_attribute.attisdropped
  UNION SELECT index_range.rngsubtype FROM pg_range index_range WHERE index_range.rngtypid=index_type.oid OR index_range.rngmultitypid=index_type.oid
  UNION SELECT index_range.rngtypid FROM pg_range index_range WHERE index_range.rngmultitypid=index_type.oid
 ) nested WHERE nested.oid<>0
), classes(oid) AS (
 SELECT unnest(index_entry.indclass) FROM pg_index index_entry WHERE index_entry.indrelid=` + relation + `
 UNION SELECT default_class.oid FROM pg_opclass default_class WHERE default_class.opcdefault AND default_class.opcintype IN(SELECT oid FROM index_types)
 UNION SELECT index_range.rngsubopc FROM pg_range index_range WHERE index_range.rngtypid IN(SELECT oid FROM index_types)
)
 SELECT 1 FROM classes indexed_class WHERE ` + unsafeClassSQL("indexed_class.oid") + `)`
}

func unsafeClassSQL(class string) string {
	return `EXISTS(SELECT 1 FROM pg_opclass qualified_class JOIN pg_opfamily qualified_family ON qualified_family.oid=qualified_class.opcfamily
 WHERE qualified_class.oid=` + class + ` AND (
 qualified_class.opcnamespace<>'pg_catalog'::regnamespace OR qualified_class.oid>=16384 OR
 qualified_family.opfnamespace<>'pg_catalog'::regnamespace OR qualified_family.oid>=16384 OR EXISTS(
 SELECT 1 FROM pg_amproc support JOIN pg_proc class_proc ON class_proc.oid=support.amproc
 WHERE support.amprocfamily=qualified_family.oid AND support.amproclefttype=qualified_class.opcintype AND support.amprocrighttype=qualified_class.opcintype
 AND (class_proc.pronamespace<>'pg_catalog'::regnamespace OR class_proc.oid>=16384)) OR EXISTS(
 SELECT 1 FROM pg_amop member JOIN pg_operator class_operator ON class_operator.oid=member.amopopr JOIN pg_proc class_proc ON class_proc.oid=class_operator.oprcode
 WHERE member.amopfamily=qualified_family.oid AND member.amoplefttype=qualified_class.opcintype AND member.amoprighttype=qualified_class.opcintype
 AND (class_operator.oprnamespace<>'pg_catalog'::regnamespace OR class_operator.oid>=16384 OR class_proc.pronamespace<>'pg_catalog'::regnamespace OR class_proc.oid>=16384))))`
}

// Range input invokes canonicalization and subtype comparison even without an
// index or an expression dependency from the business table.
func unsafeRangeTypesSQL(relation string) string {
	return `EXISTS(` + columnTypesSQL(relation) + `
 SELECT 1 FROM pg_range range_contract WHERE (range_contract.rngtypid IN(SELECT oid FROM column_types) OR range_contract.rngmultitypid IN(SELECT oid FROM column_types)) AND (
 EXISTS(SELECT 1 FROM pg_proc range_proc WHERE range_proc.oid IN(range_contract.rngcanonical,range_contract.rngsubdiff) AND
  (range_proc.pronamespace<>'pg_catalog'::regnamespace OR range_proc.oid>=16384 OR range_proc.provolatile<>'i')) OR ` + unsafeClassSQL("range_contract.rngsubopc") + `))`
}

func trustedTypeFingerprintSQL(relation string) string {
	body := embeddedFunction(typeContract, "type_fingerprint")[5]
	return strings.NewReplacer("target", relation, "physical", "false").Replace(body)
}

func typeContractMismatchSQL(relation, saved string) string {
	return "(" + saved + " IS NULL OR " + saved + " IS DISTINCT FROM (" + trustedTypeFingerprintSQL(relation) + "))"
}

func unprovenTypeSQL(operation string) string {
	return `NOT EXISTS(SELECT 1 FROM gregale_repair.operations type_proof WHERE type_proof.id=` + operation + ` AND
 (type_proof.type_fingerprint=e.type_fingerprint OR (type_proof.type_fingerprint IS NULL AND e.type_fingerprint=encode(sha256(convert_to('[]','UTF8')),'hex'))))`
}
