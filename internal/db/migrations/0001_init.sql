-- 0001_init.sql — full schema per plan §2.
--
-- Every column in §2 is created here, including the ones nothing writes until
-- P3 (categories, items.attributes, items.field_sources). P3 is additive by
-- construction.

CREATE TABLE users (
  id            TEXT PRIMARY KEY,
  username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
  display_name  TEXT NOT NULL,
  password_hash TEXT NOT NULL,              -- argon2id encoded string
  is_admin      INTEGER NOT NULL DEFAULT 0,
  must_reset    INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  legacy_id     TEXT                        -- populated by importer; nullable forever
) STRICT;

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,              -- sha256 of the cookie value
  user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  user_agent TEXT
) STRICT;
CREATE INDEX idx_sessions_user ON sessions(user_id);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

CREATE TABLE invites (
  token_hash TEXT PRIMARY KEY,
  created_by TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  used_at    TEXT,
  used_by    TEXT REFERENCES users(id)
) STRICT;

CREATE TABLE lists (
  id         TEXT PRIMARY KEY,
  owner_id   TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  visibility TEXT NOT NULL CHECK (visibility IN ('private','all_users','selected')),
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
) STRICT;
CREATE INDEX idx_lists_owner ON lists(owner_id);

CREATE TABLE list_shares (
  list_id TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  PRIMARY KEY (list_id, user_id)
) STRICT;

CREATE TABLE categories (
  id           TEXT PRIMARY KEY,
  slug         TEXT NOT NULL UNIQUE,
  label        TEXT NOT NULL,
  sort_order   INTEGER NOT NULL DEFAULT 0,
  field_schema TEXT NOT NULL DEFAULT '[]'   -- JSON array of field descriptors
) STRICT;

CREATE TABLE items (
  id            TEXT PRIMARY KEY,
  list_id       TEXT NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  category_id   TEXT REFERENCES categories(id),
  title         TEXT NOT NULL,
  url           TEXT,                        -- normalized
  url_raw       TEXT,                        -- exactly what the user pasted
  description   TEXT,
  notes         TEXT,                        -- owner-authored free text
  price_cents   INTEGER,
  currency      TEXT,
  price_seen_at TEXT,                        -- when price was captured
  quantity      INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 1),
  claimed_qty   INTEGER NOT NULL DEFAULT 0
                CHECK (claimed_qty >= 0 AND claimed_qty <= quantity),
  attributes    TEXT NOT NULL DEFAULT '{}',  -- JSON, validated against category schema
  field_sources TEXT NOT NULL DEFAULT '{}',  -- JSON: field -> 'user'|'shopify'|'jsonld'|'og'|'sidecar'
  link_status   TEXT NOT NULL DEFAULT 'unknown'
                CHECK (link_status IN ('unknown','ok','suspect','dead')),
  link_checked_at TEXT,
  sort_order    INTEGER NOT NULL DEFAULT 0,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL,
  edited_at     TEXT,                        -- last owner edit; drives the "item was edited" marker
  deleted_at    TEXT,                        -- soft delete; never hard-delete claimed items
  legacy_id     TEXT
) STRICT;
CREATE INDEX idx_items_list ON items(list_id) WHERE deleted_at IS NULL;

CREATE TABLE item_images (
  id         TEXT PRIMARY KEY,
  item_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  sha256     TEXT NOT NULL,                  -- storage key
  mime       TEXT NOT NULL,
  width      INTEGER,
  height     INTEGER,
  is_primary INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
) STRICT;
CREATE INDEX idx_item_images_item ON item_images(item_id);

CREATE TABLE claims (
  id         TEXT PRIMARY KEY,
  item_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  claimer_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  qty        INTEGER NOT NULL CHECK (qty >= 1),
  state      TEXT NOT NULL DEFAULT 'claimed'
             CHECK (state IN ('claimed','purchased')),
  note       TEXT,                           -- claimer-side; NEVER shown to list owner
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
) STRICT;
CREATE INDEX idx_claims_item ON claims(item_id);
CREATE INDEX idx_claims_claimer ON claims(claimer_id);

-- Category seed (§2.2). IDs are fixed constants so they are stable across
-- installs and importer runs.
INSERT INTO categories (id, slug, label, sort_order, field_schema) VALUES
 ('9439c2dc-626e-4090-aba8-8e1c739a2bee','general','General',10,'[]'),
 ('6ae22a97-c337-4d2c-860a-25bad54522e4','clothing','Clothing',20,
  '[{"key":"size","label":"Size","type":"text","required":false},{"key":"color","label":"Color","type":"text","required":false},{"key":"fit","label":"Fit","type":"select","required":false,"options":["mens","womens","unisex","kids"]}]'),
 ('ce88bee7-f9d4-40c8-8db3-231bc8c1224e','shoes','Shoes',30,
  '[{"key":"size","label":"Size","type":"text","required":false},{"key":"width","label":"Width","type":"text","required":false},{"key":"color","label":"Color","type":"text","required":false}]'),
 ('9e0d31a2-2fae-42f0-8969-c7a12ffb81d5','books','Books & Media',40,
  '[{"key":"format","label":"Format","type":"select","required":false,"options":["hardcover","paperback","ebook","audio"]},{"key":"edition","label":"Edition","type":"text","required":false}]'),
 ('aca4c5ae-6878-4a42-adfb-649c287547c3','toys','Toys & Games',50,
  '[{"key":"age_range","label":"Age range","type":"text","required":false},{"key":"player_count","label":"Players","type":"text","required":false}]'),
 ('604f6fc8-3d1b-41c2-bfda-4782f1fdc444','tools','Tools & Hardware',60,
  '[{"key":"brand","label":"Brand","type":"text","required":false},{"key":"voltage","label":"Voltage","type":"text","required":false},{"key":"variant","label":"Variant","type":"text","required":false}]'),
 ('fafa52ff-e4ff-4677-bf77-2cd075d1edeb','electronics','Electronics',70,
  '[{"key":"model","label":"Model","type":"text","required":false},{"key":"color","label":"Color","type":"text","required":false},{"key":"capacity","label":"Capacity","type":"text","required":false}]'),
 ('c70335e2-a975-4e0b-8cb0-ff42993f669a','kitchen','Kitchen',80,
  '[{"key":"size","label":"Size","type":"text","required":false},{"key":"color","label":"Color","type":"text","required":false},{"key":"material","label":"Material","type":"text","required":false}]'),
 ('9c0c81e1-3cb3-4bd4-9ccd-9bd5f2724727','outdoor','Outdoor & Sport',90,
  '[{"key":"size","label":"Size","type":"text","required":false},{"key":"color","label":"Color","type":"text","required":false}]'),
 ('715cb23b-6ee5-4932-b006-7fae95f1ce43','other','Other',100,'[]');
