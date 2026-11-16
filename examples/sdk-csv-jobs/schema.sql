CREATE TABLE IF NOT EXISTS sdk_csv_rows (input_id text PRIMARY KEY, row_number integer NOT NULL, email text NOT NULL);
CREATE TABLE IF NOT EXISTS sdk_csv_attempts (input_id text PRIMARY KEY, calls integer NOT NULL);
