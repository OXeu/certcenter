<script setup lang="ts">
import Button from '@oxeu/center-common/ui/Button.vue'
import { computed, ref, watch, nextTick, useId, onBeforeUnmount } from 'vue'
import { toastTarget } from './toast-host.ts'
import DropdownSelect from '@oxeu/center-common/ui/DropdownSelect.vue'
import CheckboxField from '@oxeu/center-common/ui/CheckboxField.vue'
const formId = useId()
const props = defineProps({
  form: { type: Object, required: true },
  msg: { type: Function, required: true },
})
const visibleFields = computed(() =>
  props.form.fields.filter(
    (field: any) => !field.kinds || field.kinds.includes(props.form.values.kind),
  ),
)
const canAddScope = computed(() =>
  props.form.scopeProjects.some(
    (option: import('./forms.ts').Choice) =>
      !props.form.scopes?.some(
        (scope: import('./forms.ts').ScopeDraft) => scope.project === option.value,
      ),
  ),
)
const dialog = ref<HTMLDialogElement | null>(null)
watch(
  () => props.form.open,
  async (open) => {
    await nextTick()
    if (open && dialog.value) {
      dialog.value.showModal()
      toastTarget.value = dialog.value
    } else {
      releaseToasts()
      dialog.value?.close()
    }
  },
  { immediate: true },
)
function releaseToasts() {
  if (toastTarget.value === dialog.value) toastTarget.value = 'body'
}
onBeforeUnmount(releaseToasts)
function cancel(event: Event) {
  if (props.form.busy) {
    event.preventDefault()
    return
  }
  props.form.open = false
}
</script>
<template>
  <dialog ref="dialog" class="dialog-panel" :aria-label="form.title" @cancel="cancel">
    <form class="space-y-3" @submit.prevent="form.submit">
      <h2 class="text-base font-semibold">{{ form.title }}</h2>
      <template v-if="!form.token">
        <div v-for="field in visibleFields" :key="field.key" class="field">
          <label v-if="!field.choices" :for="`${formId}-${field.key}`">{{
            msg(field.label)
          }}</label>
          <DropdownSelect
            v-if="field.options"
            :id="`${formId}-${field.key}`"
            v-model="form.values[field.key]"
            :label="msg(field.label)"
            :options="field.options"
            :required="field.required"
            :disabled="form.busy || field.readonly"
          />
          <div
            v-else-if="field.choices"
            class="space-y-1"
            role="group"
            :aria-label="msg(field.label)"
          >
            <p>{{ msg(field.label) }}</p>
            <CheckboxField
              v-for="choice in field.choices"
              :key="choice.value"
              v-model="form.selections[field.key]"
              :label="choice.label"
              :value="choice.value"
              :disabled="form.busy"
            />
          </div>
          <textarea
            v-else-if="field.multiline"
            :id="`${formId}-${field.key}`"
            v-model="form.values[field.key]"
            class="input"
            :required="field.required"
            :disabled="form.busy"
          ></textarea>
          <input
            v-else-if="field.secret"
            :id="`${formId}-${field.key}`"
            v-model="form.values[field.key]"
            type="password"
            autocomplete="new-password"
            class="input"
            :required="field.required"
            :disabled="form.busy"
          />
          <input
            v-else
            :id="`${formId}-${field.key}`"
            v-model="form.values[field.key]"
            class="input"
            :required="field.required"
            :disabled="form.busy"
          />
          <p v-if="field.help" class="field">{{ msg(field.help) }}</p>
        </div>
        <section v-if="form.scopes !== null" class="space-y-3">
          <h3 class="text-sm font-medium">{{ msg('keys.scopes') }}</h3>
          <p class="field">{{ msg(form.scopeHelp) }}</p>
          <fieldset
            v-for="scope in form.scopes"
            :key="scope.id"
            class="card space-y-3"
            :aria-label="msg(form.scopeLabel)"
          >
            <div class="flex items-center gap-2">
              <DropdownSelect
                :id="`${formId}-scope-${scope.id}`"
                v-model="scope.project"
                @update:model-value="form.limitScope(scope)"
                class="min-w-0 flex-1"
                :label="msg(form.scopeLabel)"
                :options="form.scopeProjects"
                :disabled="form.busy"
                required
              />
              <Button
                type="button"
                :aria-label="msg('keys.excludeScope')"
                :disabled="form.busy"
                @click="form.excludeScope(scope.id)"
                variant="secondary"
                content="icon"
                icon="ri-close-line"
              >
              </Button>
            </div>
            <div class="grid gap-1 sm:grid-cols-2">
              <CheckboxField
                v-for="permission in form.permissionsFor(scope)"
                :key="permission.value"
                v-model="scope.permissions"
                :label="permission.label"
                :value="permission.value"
                :disabled="form.busy"
              />
            </div>
          </fieldset>
          <Button
            v-if="canAddScope"
            type="button"
            :disabled="form.busy"
            @click="form.addScope()"
            variant="secondary"
          >
            {{ msg('keys.addScope') }}
          </Button>
        </section>
      </template>
      <p v-if="form.token" class="field">
        {{ msg('projects.tokenIssued') }}<code class="break-all">{{ form.token }}</code>
      </p>
      <div class="flex flex-wrap justify-end gap-2">
        <Button type="button" :disabled="form.busy" @click="form.open = false" variant="ghost">
          {{ msg('common.cancel') }}
        </Button>
        <Button
          v-if="!form.token"
          :variant="form.danger ? 'danger' : 'primary'"
          type="submit"
          :disabled="form.busy"
        >
          {{ msg('confirm.ok') }}
        </Button>
      </div>
    </form>
  </dialog>
</template>
