const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const script = fs.readFileSync('app/static/native.js', 'utf8');
const start = script.indexOf('function nativeNewestFirst');
const end = script.indexOf('function nativeChatForm', start);
assert.ok(start >= 0 && end > start);
const context = {};
vm.runInNewContext(script.slice(start, end), context);
const ids = rows => Array.from(rows, row => row.id);

test('tasks sort by creation time with unknown dates last, without mutating API rows', () => {
  const rows = [{id:'old',created_at:'2026-01-01T00:00:00Z'},{id:'new',created_at:'2026-11-01T00:00:00Z'},{id:'unknown'}];
  assert.deepEqual(ids(context.nativeNewestFirst(rows,'assistant_tasks')), ['new','old','unknown']);
  assert.deepEqual(ids(rows), ['old','new','unknown']);
});
test('memory uses last update; subscriptions reverse creation order', () => {
  assert.deepEqual(ids(context.nativeNewestFirst([{id:'a',updated_at:'2026-01-01T00:00:00Z'},{id:'b',updated_at:'2026-10-01T00:00:00Z'}],'assistant_memory')), ['b','a']);
  assert.deepEqual(ids(context.nativeNewestFirst([{id:'a'},{id:'b'}],'assistant_subscriptions')), ['b','a']);
});
