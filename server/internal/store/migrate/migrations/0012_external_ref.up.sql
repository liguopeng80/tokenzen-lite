-- 0012 外部标识：接入方侧的对象标识，写入后不可变，按接入方隔离唯一。
-- 用于两侧对象长期对应：username 会改名、数字 ID 单向不可反查，
-- external_ref 由接入方提供、网关侧按它精确检索。
-- 设计依据 docs/glossary.md 的「外部标识 external_ref」节。

ALTER TABLE users       ADD COLUMN external_ref TEXT NOT NULL DEFAULT '';
ALTER TABLE departments ADD COLUMN external_ref TEXT NOT NULL DEFAULT '';

-- 按接入方隔离唯一：运营方对象 integration_id 为 NULL 且 external_ref 默认空串，
-- 不进部分索引（WHERE external_ref <> ''），故运营方对象互不冲突。
-- NULL integration_id 不会出现在本索引中（只有接入方对象且填了 ref 才进）。
CREATE UNIQUE INDEX users_external_ref_uniq
    ON users (integration_id, external_ref) WHERE external_ref <> '';
CREATE UNIQUE INDEX departments_external_ref_uniq
    ON departments (integration_id, external_ref) WHERE external_ref <> '';

-- 写入后不可变：应用层 UpdateFields 白名单不含 external_ref，触发器兜底（仿 audit_logs 0006）。
CREATE OR REPLACE FUNCTION external_ref_immutable() RETURNS TRIGGER AS $$
BEGIN
    IF NEW.external_ref IS DISTINCT FROM OLD.external_ref THEN
        RAISE EXCEPTION '% external_ref 写入后不可变更', TG_TABLE_NAME;
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_external_ref_immutable
    BEFORE UPDATE ON users
    FOR EACH ROW EXECUTE FUNCTION external_ref_immutable();

CREATE TRIGGER departments_external_ref_immutable
    BEFORE UPDATE ON departments
    FOR EACH ROW EXECUTE FUNCTION external_ref_immutable();
