package datarepair

import "strings"

// Catalog inspection only: never invoke application expression functions to
// decide whether a table belongs to the supported capture/repair contract.
func unsupportedExpressionsSQL(relation string) string {
	return "(" + unsafeIndexClassesSQL(relation) + " OR " + unsafeRangeTypesSQL(relation) + " OR " + strings.ReplaceAll(`EXISTS(
`+columnTypesSQL(relation)+`
 SELECT 1 FROM pg_depend d
 LEFT JOIN pg_operator expression_operator ON d.refclassid='pg_operator'::regclass AND expression_operator.oid=d.refobjid
 JOIN pg_proc expression_proc ON expression_proc.oid=CASE WHEN d.refclassid='pg_proc'::regclass THEN d.refobjid ELSE expression_operator.oprcode END
 JOIN pg_namespace n ON n.oid=expression_proc.pronamespace
 WHERE d.refclassid IN ('pg_proc'::regclass,'pg_operator'::regclass) AND
  (n.nspname<>'pg_catalog' OR expression_proc.provolatile<>'i' OR
   (expression_operator.oid IS NOT NULL AND expression_operator.oprnamespace<>'pg_catalog'::regnamespace)) AND
  ((d.classid='pg_constraint'::regclass AND d.objid IN(SELECT oid FROM pg_constraint WHERE conrelid=__RELATION__ OR contypid IN(SELECT oid FROM column_types))) OR
   (d.classid='pg_attrdef'::regclass AND d.objid IN(SELECT oid FROM pg_attrdef WHERE adrelid=__RELATION__)) OR
   (d.classid='pg_class'::regclass AND d.objid IN(SELECT indexrelid FROM pg_index WHERE indrelid=__RELATION__))))`, "__RELATION__", relation) + ")"
}

func columnTypesSQL(relation string) string {
	return strings.ReplaceAll(` WITH RECURSIVE column_types(oid) AS (
  SELECT atttypid FROM pg_attribute WHERE attrelid=__RELATION__ AND attnum>0 AND NOT attisdropped
  UNION
  SELECT nested.oid FROM column_types ct JOIN pg_type t ON t.oid=ct.oid
  CROSS JOIN LATERAL (
   SELECT t.typbasetype AS oid WHERE t.typtype='d'
   UNION SELECT t.typelem WHERE t.typcategory='A'
   UNION SELECT atttypid FROM pg_attribute WHERE attrelid=t.typrelid AND attnum>0 AND NOT attisdropped
   UNION SELECT nested_range.rngsubtype FROM pg_range nested_range WHERE nested_range.rngtypid=t.oid OR nested_range.rngmultitypid=t.oid
   UNION SELECT nested_range.rngtypid FROM pg_range nested_range WHERE nested_range.rngmultitypid=t.oid
  ) nested WHERE nested.oid<>0
 )
`, "__RELATION__", relation)
}
