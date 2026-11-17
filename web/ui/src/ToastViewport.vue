<script setup lang="ts">
import { onUnmounted, shallowRef } from 'vue'
import { toast } from '@yusufceng1z/ding'
import ToastItem from './ToastItem.vue'
import { toastTarget } from './toast-host.ts'

const state = shallowRef(toast.getState())
const unsubscribe = toast.subscribe(() => {
  state.value = toast.getState()
})
onUnmounted(unsubscribe)
function pause() {
  toast.pause()
}
function resume() {
  toast.resume()
}
function dismiss() {
  toast.dismiss()
}
</script>
<template>
  <Teleport :to="toastTarget">
    <div
      class="tst-viewport"
      role="region"
      :aria-label="state.labels.region"
      :data-theme="state.theme"
      :data-position="state.position"
      @pointerenter="pause"
      @pointerleave="resume"
      @focusin="pause"
      @focusout="resume"
      @keydown.esc.stop.prevent="dismiss"
    >
      <div class="tst-sr" role="status">{{ state.status }}</div>
      <div class="tst-sr" role="alert">{{ state.alert }}</div>
      <ToastItem
        v-for="(item, index) in state.toasts"
        :key="item.id"
        :item="item"
        :index="state.toasts.length - 1 - index"
        :dismiss-label="state.labels.dismiss"
      />
    </div>
  </Teleport>
</template>
