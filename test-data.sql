-- テスト用のサンプルテーブルとデータを作成
CREATE TABLE IF NOT EXISTS users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE,
    email VARCHAR(255) NOT NULL UNIQUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS posts (
    id SERIAL PRIMARY KEY,
    user_id INTEGER REFERENCES users(id),
    content TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- サンプルユーザーデータを挿入
INSERT INTO users (username, email) VALUES
    ('testuser1', 'test1@example.com'),
    ('testuser2', 'test2@example.com'),
    ('testuser3', 'test3@example.com')
ON CONFLICT (username) DO NOTHING;

-- サンプル投稿データを挿入
INSERT INTO posts (user_id, content) VALUES
    (1, 'This is a test post from user 1'),
    (2, 'Another test post from user 2'),
    (3, 'Third test post from user 3'),
    (1, 'Second post from user 1'),
    (2, 'Second post from user 2');

-- データベースの統計情報を更新
ANALYZE;
