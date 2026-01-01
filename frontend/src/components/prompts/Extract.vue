<template>
  <div class="card floating" id="extract">
    <div class="card-title">
      <h2>{{ t("prompts.extract") }}</h2>
    </div>

    <div class="card-content">
      <p>{{ t("prompts.extractMessage") }}</p>

      <button
        id="focus-prompt"
        class="button button--block"
        @click="extractHere"
      >
        {{ t("buttons.extractHere") }}
      </button>
      <button
        class="button button--block"
        @click="extractToFolder"
      >
        {{ t("buttons.extractToFolder") }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { inject } from "vue";
import { useI18n } from "vue-i18n";
import { useLayoutStore } from "@/stores/layout";
import { useFileStore } from "@/stores/file";
import { files as api } from "@/api";

const layoutStore = useLayoutStore();
const fileStore = useFileStore();
const { t } = useI18n();

const $showError = inject<IToastError>("$showError")!;

const extractHere = async () => {
  if (fileStore.req === null || fileStore.selectedCount !== 1) return;

  const file = fileStore.req.items[fileStore.selected[0]];
  if (!file || file.isDir) return;

  layoutStore.closeHovers();
  layoutStore.loading = true;
  try {
    await api.extract(file.url, "here");
    fileStore.reload = true;
  } catch (e: any) {
    $showError(e);
  } finally {
    layoutStore.loading = false;
  }
};

const extractToFolder = async () => {
  if (fileStore.req === null || fileStore.selectedCount !== 1) return;

  const file = fileStore.req.items[fileStore.selected[0]];
  if (!file || file.isDir) return;

  layoutStore.closeHovers();
  layoutStore.loading = true;
  try {
    await api.extract(file.url, "subdir");
    fileStore.reload = true;
  } catch (e: any) {
    $showError(e);
  } finally {
    layoutStore.loading = false;
  }
};
</script>
