import { shallowRef } from 'vue'

export const toastTarget = shallowRef<HTMLElement | string>('body')
