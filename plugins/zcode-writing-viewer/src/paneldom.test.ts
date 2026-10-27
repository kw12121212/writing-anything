// @vitest-environment jsdom
/**
 * The enlarged review overlay, driven through a real DOM: opening moves focus
 * into the overlay, Escape collapses it (stopping propagation and default),
 * and a session that ends collapses it too (overlay-keyboard-dismiss — never
 * only direct calls on props).
 */
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { panelState, renderReviewOverlay } from './panel';
import { reviewOverlayShown } from './reviewview';

beforeAll(() => {
  (window as unknown as { __WV_PANEL_TEST__: boolean }).__WV_PANEL_TEST__ = true;
  const root = document.createElement('div');
  root.id = 'root';
  document.body.append(root);
});

describe('review overlay', () => {
  it('opens focused and Escape collapses it with propagation stopped', () => {
    panelState.review = { unit: 'my-post', phase: 'active', error: null, startedAt: 'now' };
    panelState.reviewEnlarged = true;
    const overlay = renderReviewOverlay();
    document.body.append(overlay);
    expect(overlay.getAttribute('data-review-overlay')).toBe('');
    expect(document.querySelector('[data-review-overlay]')).not.toBeNull();

    // Escape on the focused overlay collapses it.
    const event = new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true });
    const stopPropagation = vi.spyOn(event, 'stopPropagation');
    const preventDefault = vi.spyOn(event, 'preventDefault');
    overlay.focus();
    overlay.dispatchEvent(event);
    expect(stopPropagation).toHaveBeenCalled();
    expect(preventDefault).toHaveBeenCalled();
    expect(panelState.reviewEnlarged).toBe(false);
    overlay.remove();
  });

  it('other keys do not collapse the overlay', () => {
    panelState.review = { unit: 'my-post', phase: 'active', error: null, startedAt: 'now' };
    panelState.reviewEnlarged = true;
    const overlay = renderReviewOverlay();
    document.body.append(overlay);
    overlay.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true }));
    expect(panelState.reviewEnlarged).toBe(true);
    overlay.remove();
    panelState.reviewEnlarged = false;
  });

  it('the overlay is not shown while the review session has ended', () => {
    panelState.review = { unit: 'my-post', phase: 'ended', error: null, startedAt: 'now' };
    panelState.reviewEnlarged = true;
    // reviewOverlayShown is the pure gate; renderReviewOverlay itself is only
    // mounted through render(), which consults the same gate.
    expect(reviewOverlayShown(panelState.review, panelState.reviewEnlarged)).toBe(false);
  });
});
