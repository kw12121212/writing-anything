import { describe, expect, it } from 'vitest';
import {
  IDLE_REVIEW,
  REVIEW_FRAME_SRC,
  reviewAfterStart,
  reviewAfterStatus,
  reviewLabelsFor,
  reviewStartBody,
  reviewViewModel,
} from './reviewview';

const zh = reviewLabelsFor('zh-CN');
const en = reviewLabelsFor('en');

describe('review view model', () => {
  it('offers the Review button while idle', () => {
    const model = reviewViewModel(IDLE_REVIEW, 'my-post', en, 'h:1');
    expect(model).toMatchObject({ showStart: true, startDisabled: false, frame: null, showEnd: false, errorText: null, endedText: null, busyText: null });
  });

  it('shows progress while starting and blocks a second start', () => {
    const state = { ...IDLE_REVIEW, unit: 'my-post', phase: 'starting' as const };
    const model = reviewViewModel(state, 'my-post', zh, 'h:1');
    expect(model.busyText).toBe('正在启动阅读页…');
    expect(model.startDisabled).toBe(true);
    // Another unit's detail still sees the in-flight start as a block.
    expect(reviewViewModel(state, 'my-novel', zh, 'h:1').startDisabled).toBe(true);
  });

  it('embeds the proxied reading view with an end button once active (panel-embed)', () => {
    const state = reviewAfterStart('my-post', 200, { ok: true, session: { unit: 'my-post', root: '/r', startedAt: 't1' } });
    expect(state.phase).toBe('active');
    const model = reviewViewModel(state, 'my-post', en, 'h:1');
    expect(model.frame).toEqual({ src: REVIEW_FRAME_SRC, key: 'review-t1', title: en.frameTitle });
    expect(REVIEW_FRAME_SRC.endsWith('/')).toBe(true);
    expect(model.showEnd).toBe(true);
    expect(model.showStart).toBe(false);
    // Another unit's detail shows a plain Review button, not this iframe.
    expect(reviewViewModel(state, 'my-novel', en, 'h:1')).toMatchObject({ frame: null, showStart: true, showEnd: false });
  });

  it('names the authority to allow when the origin guard refused (panel-origin-rejected)', () => {
    const state = reviewAfterStart('my-post', 403, { ok: false, error: 'Host 不在放行范围' });
    expect(state).toMatchObject({ phase: 'error', forbidden: true });
    const model = reviewViewModel(state, 'my-post', zh, 'gamepc.tail1234.ts.net:13080');
    expect(model.errorText).toContain('allowedHosts');
    expect(model.errorText).toContain('gamepc.tail1234.ts.net:13080');
    expect(model.errorText).toContain('Host 不在放行范围');
    expect(model.frame).toBeNull();
    expect(model.showStart).toBe(true);
  });

  it('shows a start failure readably and keeps the retry entry', () => {
    const state = reviewAfterStart('my-post', 200, { ok: false, error: '找不到单元 my-post' });
    const model = reviewViewModel(state, 'my-post', zh, 'h');
    expect(model.errorText).toBe('审阅启动失败：找不到单元 my-post');
    expect(model.showStart).toBe(true);
    expect(reviewAfterStart('u', 500, null)).toMatchObject({ phase: 'error', error: 'HTTP 500', forbidden: false });
  });

  it('ends a review whose session was replaced for another unit (panel-session-replaced-on-focus)', () => {
    const active = reviewAfterStart('my-post', 200, { ok: true, session: { unit: 'my-post', startedAt: 't1' } });
    // Another tab started my-post/intro: the one session now belongs to it.
    const replaced = reviewAfterStatus(active, { ok: true, session: { unit: 'my-post/intro', startedAt: 't2' } });
    expect(replaced.phase).toBe('ended');
    expect(reviewViewModel(replaced, 'my-post', zh, 'h').endedText).toContain('审阅已结束');
    // A malformed summary without a unit degrades the same safe way.
    expect(reviewAfterStatus(active, { ok: true, session: {} }).phase).toBe('ended');
  });

  it('turns an active review whose session is gone into an ended notice (session-gone)', () => {
    const active = reviewAfterStart('my-post', 200, { ok: true, session: { unit: 'my-post', root: '/r', startedAt: 't1' } });
    expect(reviewAfterStatus(active, { ok: true, session: { unit: 'my-post' } })).toBe(active);
    const ended = reviewAfterStatus(active, { ok: true, session: null });
    expect(ended.phase).toBe('ended');
    const model = reviewViewModel(ended, 'my-post', zh, 'h');
    expect(model.endedText).toContain('审阅已结束');
    expect(model.frame).toBeNull();
    expect(model.showStart).toBe(true);
    expect(reviewAfterStatus(IDLE_REVIEW, { session: null })).toBe(IDLE_REVIEW);
  });

  it('builds the start body with the displayed root when known', () => {
    expect(JSON.parse(reviewStartBody('my-post', '/home/me/book'))).toEqual({ unit: 'my-post', root: '/home/me/book' });
    expect(JSON.parse(reviewStartBody('my-post', null))).toEqual({ unit: 'my-post' });
  });
});
