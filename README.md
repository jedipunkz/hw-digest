# hw-digest

Vercel で配信するハードウェアニュースダイジェストです。

- `/akiba/` — 日本のハードウェア情報（RSS は `/akiba/index.xml`）
- `/world/` — PC自作・コンポーネント・モバイル・メイカー系デバイス中心の海外情報（RSS は `/world/index.xml`）

`sources.json` に収集元を定義しています。GitHub Actions が1時間ごとに実行され、公開日時が直近1時間以内の記事のみを収集して `docs/` を生成・コミットします。`workflow_dispatch` では収集対象期間として `1h` / `3h` / `24h` / `7days` を選択できます。push をトリガーに Vercel が再デプロイします。`data/seen.json` はURLのSHA-256を30日保持するため、同じ記事を再配信しません。

日本側は、自作PC／パーツのエルミタージュ秋葉原・4Gamer・北森瓦版・PC Watch・AKIBA PC Hotline!・ニッチなPCゲーマーの環境構築Z、小型PC／ガジェットのこまめブログ・すまほん!!・iPhone Mania・gori.me、オーディオの e☆イヤホンを収集します。

海外側は、PCパーツ／自作PCを主題にする TechPowerUp・Tom's Hardware・GamersNexus・KitGuru・TweakTown・igor'sLAB、ノートPC／スマートフォンを主題にする Notebookcheck・Android Authority・XDA、ハードウェア製品記事を持つ Ars Technica Gear & Gadgets、サーバ／ホームラボの ServeTheHome・Jeff Geerling、メイカー／組み込み／ニッチデバイスの Hackaday・CNX Software・Raspberry Pi News・Hackster.io・Arduino Blog・Good e-Reader を選定しています。

## トークン保護された配信

すべてのページと RSS は `middleware.js`（Vercel Edge Middleware）で保護され、リクエストが正しい `?token=` クエリを持たない限り `403` を返します。トークンは `RSS_TOKEN` 環境変数と照合されます。静的ファイルではなくサーバ（エッジ）側で検証するため、RSS リーダー（Inoreader 等）でも購読できます。

セットアップ:

1. Vercel にプロジェクトを接続（このリポジトリを import）。`vercel.json` により `docs/` を静的配信します。

2. Vercel に環境変数を登録（最低でも Production スコープ）:

   ```sh
   vercel env add RSS_TOKEN production
   ```

   長いランダム値を使ってください。例: `openssl rand -hex 32`

3. RSS リーダーにトークン付き URL を登録:

   ```
   https://<your-app>.vercel.app/akiba/index.xml?token=<RSS_TOKEN>
   https://<your-app>.vercel.app/world/index.xml?token=<RSS_TOKEN>
   ```

   ブラウザで読む場合は `https://<your-app>.vercel.app/world/?token=<RSS_TOKEN>` のようにアクセスします。ページ内のリンクは現在の `?token=` を引き継ぎます。

トークンのローテーション: Vercel の `RSS_TOKEN` を更新して再デプロイし、新しい URL で購読し直します。古い URL は即座に無効になります。

## ローカル実行

```sh
go test ./...
go run ./cmd/hw-digest
```
