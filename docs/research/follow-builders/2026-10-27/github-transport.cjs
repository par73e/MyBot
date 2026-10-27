// Transport adapter for the existing Friday server. No credentials are used.
// The original raw.githubusercontent.com route is unavailable on this host.
const originalFetch = globalThis.fetch;
const prefix = 'https://raw.githubusercontent.com/zarazhangrui/follow-builders/main/';
const allowed = new Set([
  'feed-x.json', 'feed-podcasts.json', 'feed-blogs.json',
  'prompts/summarize-podcast.md', 'prompts/summarize-tweets.md',
  'prompts/summarize-blogs.md', 'prompts/digest-intro.md', 'prompts/translate.md'
]);
globalThis.fetch = async (input, init = {}) => {
  const url = String(input);
  const path = url.startsWith(prefix) ? url.slice(prefix.length) : '';
  if (!allowed.has(path)) throw new Error('Follow Builders transport: unsupported URL');
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/vnd.github.raw+json');
  headers.set('User-Agent', 'Friday-Follow-Builders');
  const response = await originalFetch(
    'https://api.github.com/repos/zarazhangrui/follow-builders/contents/' + path + '?ref=main',
    { ...init, headers, signal: init.signal || AbortSignal.timeout(15000) }
  );
  if (!response.ok) throw new Error('Follow Builders HTTP ' + response.status + ': ' + path);
  return response;
};
