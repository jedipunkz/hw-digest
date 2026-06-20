// Vercel Edge Middleware: gate every page and RSS feed behind a token.
// A request must carry the correct `?token=` query parameter, compared against
// the `RSS_TOKEN` environment variable, otherwise it receives a 403.
export const config = {
  matcher: ['/((?!_vercel/).*)'],
};

export default function middleware(request) {
  const token = new URL(request.url).searchParams.get('token');
  const expected = process.env.RSS_TOKEN;

  if (!expected || token !== expected) {
    return new Response('Forbidden', {
      status: 403,
      headers: { 'content-type': 'text/plain; charset=utf-8' },
    });
  }
  // token 一致時は何も返さず静的ファイルの配信を継続
}
