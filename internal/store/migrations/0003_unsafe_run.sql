CREATE TABLE unsafe_run (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    state TEXT NOT NULL CHECK (state IN ('clean', 'unsafe')),
    repair_bits INTEGER NOT NULL CHECK (typeof(repair_bits) = 'integer' AND repair_bits BETWEEN 0 AND 31)
);
INSERT INTO unsafe_run (id, state, repair_bits) VALUES (1, 'unsafe', 0);
