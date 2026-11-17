<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { bindToast, toast } from '@yusufceng1z/ding'

const props = defineProps<{
  item: ReturnType<typeof toast.getState>['toasts'][number]
  index: number
  dismissLabel: string
}>()
const slot = ref<HTMLElement | null>(null)
const icons = {
  success: 'ri-checkbox-circle-line',
  error: 'ri-error-warning-line',
  warning: 'ri-alert-line',
  info: 'ri-information-line',
  loading: 'ri-loader-4-line animate-spin',
}
const icon = computed(() => icons[props.item.type])
const sticky = computed(() => props.item.duration === 0 && props.item.type !== 'loading')
const tabindex = computed(() => (props.item.phase === 'open' ? 0 : -1))
const position = computed(() => ({ '--tst-i': props.index }))
const lifetime = computed(() => ({ '--tst-life': `${props.item.duration}ms` }))
let unbind: (() => void) | undefined
onMounted(() => {
  if (slot.value) unbind = bindToast(slot.value, props.item.id)
})
onUnmounted(() => unbind?.())
function dismiss() {
  toast.dismiss(props.item.id)
}
</script>
<template>
  <div ref="slot" class="tst-slot" tabindex="-1" :style="position">
    <div class="tst" :data-phase="item.phase" :data-type="item.type" :style="lifetime">
      <div class="tst-icon"><i :class="icon" aria-hidden="true"></i></div>
      <div class="tst-body">
        <div class="tst-copy">
          <div class="tst-title">
            {{ item.title }}<span v-if="item.count > 1" class="tst-count">×{{ item.count }}</span>
          </div>
          <div v-if="item.description" class="tst-desc">{{ item.description }}</div>
        </div>
        <button
          v-if="sticky"
          class="tst-close btn-ghost btn-icon"
          type="button"
          :aria-label="dismissLabel"
          :tabindex="tabindex"
          @click="dismiss"
        >
          <i class="ri-close-line" aria-hidden="true"></i>
        </button>
      </div>
    </div>
  </div>
</template>
