import { devPages as commonPages } from '@oxeu/center-common/tools/dev-pages.ts'

export const pageOptions = {
  backend: 'http://127.0.0.1:8081',
  home: '/domains',
  routes:
    /^\/(?:domains(?:\/[^?]*)?|projects(?:\/[^?]*)?|dns-providers|upstreams|users|settings|keys|login)(?:\?|$)/u,
}

export function devPages() {
  return commonPages(pageOptions)
}
