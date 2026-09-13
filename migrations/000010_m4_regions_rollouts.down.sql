BEGIN;

DROP TABLE IF EXISTS adapter_shadow_comparisons;
DROP INDEX IF EXISTS adapter_rollouts_one_shadow_per_source_uq;
DROP TABLE IF EXISTS adapter_rollouts;
DROP TABLE IF EXISTS source_ownership;
DROP TABLE IF EXISTS regions;

COMMIT;
