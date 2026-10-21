import { describe, expect, it } from 'vitest';
import { registryPaths } from './registry';

describe('registryPaths', () => {
  it('reads the verified 0.2.x registry shape (list() of entities with id, path, title)', () => {
    const summaries = registryPaths({
      list: () => [
        { id: 'w1', path: '/proj/one', title: 'One' },
        { id: 'w2', path: '/proj/two', title: 'Two' },
      ],
    });
    expect(summaries).toEqual([
      { id: 'w1', path: '/proj/one', title: 'One' },
      { id: 'w2', path: '/proj/two', title: 'Two' },
    ]);
  });

  it('falls back to the path for missing id or title', () => {
    expect(registryPaths({ list: () => [{ path: '/proj' }] })).toEqual([{ id: '/proj', path: '/proj', title: '/proj' }]);
    expect(registryPaths({ list: () => [{ id: '  ', path: '/proj', title: '  ' }] })).toEqual([
      { id: '/proj', path: '/proj', title: '/proj' },
    ]);
  });

  it('skips rows without a usable path', () => {
    expect(registryPaths({ list: () => [{ path: '  ' }, { path: 7 }, null, { path: '/ok' }] })).toEqual([
      { id: '/ok', path: '/ok', title: '/ok' },
    ]);
  });

  it('returns empty for absent registries, a missing list, a throwing list, or a non-array result', () => {
    expect(registryPaths(null)).toEqual([]);
    expect(registryPaths(undefined)).toEqual([]);
    expect(registryPaths({} as never)).toEqual([]);
    expect(
      registryPaths({
        list: () => {
          throw new Error('registry not started');
        },
      }),
    ).toEqual([]);
    expect(registryPaths({ list: () => ('nope' as unknown) as never[] })).toEqual([]);
  });
});
