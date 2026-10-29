-- +goose Up
-- Company catalog (F3, M17): the departments and job positions the company defines, by exact
-- name. They grant nothing; employees and starter packages name an entry. Entries are created
-- and retired, never deleted or renamed; whoever holds a retired entry keeps it. The service
-- refuses a new name that differs from an existing one only by case; the database keys on the
-- exact name, so spellings already in use before the catalog existed are all kept.

CREATE TABLE departments (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       text        NOT NULL UNIQUE CHECK (btrim(name) = name AND name <> ''),
    active     boolean     NOT NULL DEFAULT true,
    created_by uuid        REFERENCES employees (id), -- NULL: in use before the catalog existed
    created_at timestamptz NOT NULL DEFAULT now(),
    version    integer     NOT NULL DEFAULT 1
);

CREATE TABLE job_positions (
    id         uuid        PRIMARY KEY DEFAULT uuidv7(),
    name       text        NOT NULL UNIQUE CHECK (btrim(name) = name AND name <> ''),
    active     boolean     NOT NULL DEFAULT true,
    created_by uuid        REFERENCES employees (id),
    created_at timestamptz NOT NULL DEFAULT now(),
    version    integer     NOT NULL DEFAULT 1
);

INSERT INTO departments (name)
SELECT department FROM employees WHERE department IS NOT NULL
UNION SELECT department FROM starter_packages;

INSERT INTO job_positions (name)
SELECT job_title FROM employees WHERE job_title IS NOT NULL
UNION SELECT job_title FROM starter_packages WHERE job_title IS NOT NULL;

ALTER TABLE employees
    ADD FOREIGN KEY (department) REFERENCES departments (name),
    ADD FOREIGN KEY (job_title) REFERENCES job_positions (name);
ALTER TABLE starter_packages
    ADD FOREIGN KEY (department) REFERENCES departments (name),
    ADD FOREIGN KEY (job_title) REFERENCES job_positions (name);

GRANT SELECT, INSERT, UPDATE ON departments, job_positions TO accessflow_app;

-- One-workbook onboarding (D10 extension): employees, tools and existing access from one
-- Excel file. Spreadsheet-reported access is told apart from access the tool itself showed:
-- tool_verified_at is set when the tool read finds the grant's account and level.
ALTER TABLE imports
    DROP CONSTRAINT imports_source_check,
    DROP CONSTRAINT imports_check,
    ADD CHECK (source IN ('MOCK', 'CSV', 'WORKBOOK')),
    ADD CHECK ((source IN ('CSV', 'WORKBOOK')) = (file_name IS NOT NULL));

ALTER TABLE grants ADD COLUMN tool_verified_at timestamptz CHECK (tool_verified_at IS NULL OR import_id IS NOT NULL);
-- Grants from earlier tool reads were observed in the tool when they were imported.
UPDATE grants g SET tool_verified_at = i.imported_at FROM imports i WHERE g.import_id = i.id AND i.source = 'MOCK';
