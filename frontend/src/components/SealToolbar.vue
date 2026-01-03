<template>
  <!-- 移动端折叠按钮 -->
  <button
    v-if="isMobile"
    class="seal-toggle-btn"
    :class="{ collapsed: isCollapsed }"
    @click="toggleCollapsed"
    :title="isCollapsed ? t('seal.expand') : t('seal.collapse')"
  >
    <i class="material-icons">{{ isCollapsed ? 'smart_toy' : 'close' }}</i>
  </button>

  <div id="seal-toolbar" :class="{ hidden: shouldHide, collapsed: isMobile && isCollapsed }">
    <div class="seal-toolbar-content">
      <span class="seal-toolbar-title">{{ t("seal.title") }}</span>
      <div class="seal-toolbar-actions">
        <button
          @click="confirmImportBackup"
          :title="t('seal.importBackup')"
          class="seal-action"
          :disabled="isImporting"
        >
          <i class="material-icons">{{ isImporting ? 'hourglass_empty' : 'upload_file' }}</i>
          <span>{{ isImporting ? t("seal.importing") : t("seal.importBackup") }}</span>
        </button>
        <button
          @click="downloadLatestBackup"
          :title="t('seal.downloadLatest')"
          class="seal-action"
          :disabled="isDownloading"
        >
          <i class="material-icons">{{ isDownloading ? 'hourglass_empty' : 'cloud_download' }}</i>
          <span>{{ t("seal.downloadLatest") }}</span>
        </button>
        <button
          @click="viewBackups"
          :title="t('seal.viewBackups')"
          class="seal-action"
        >
          <i class="material-icons">folder_open</i>
          <span>{{ t("seal.viewBackups") }}</span>
        </button>
      </div>
    </div>
    <input
      type="file"
      ref="fileInput"
      @change="handleFileSelect"
      accept=".zip"
      style="display: none"
    />
  </div>

  <!-- 导入备份警告弹框 -->
  <div v-if="showImportWarning" class="seal-warning-overlay" @click.self="showImportWarning = false">
    <div class="seal-warning-dialog">
      <div class="seal-warning-header">
        <i class="material-icons warning-icon">warning</i>
        <span>{{ t("seal.importWarningTitle") }}</span>
      </div>
      <div class="seal-warning-content">
        <p>{{ t("seal.importWarningMessage") }}</p>
        <ul>
          <li>{{ t("seal.importWarningItem1") }}</li>
          <li>{{ t("seal.importWarningItem2") }}</li>
          <li>{{ t("seal.importWarningItem3") }}</li>
        </ul>
        <p class="seal-warning-disclaimer">{{ t("seal.importWarningDisclaimer") }}</p>
      </div>
      <div class="seal-warning-actions">
        <button class="seal-warning-btn cancel" @click="showImportWarning = false">
          {{ t("buttons.cancel") }}
        </button>
        <button class="seal-warning-btn confirm" @click="proceedImport">
          {{ t("seal.confirmImport") }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, inject } from "vue";
import { useRouter } from "vue-router";
import { useI18n } from "vue-i18n";
import { useFileStore } from "@/stores/file";
import { files as api } from "@/api";
import { fetchURL } from "@/api/utils";
import { baseURL } from "@/utils/constants";
import { useAuthStore } from "@/stores/auth";

const { t } = useI18n();
const router = useRouter();
const fileStore = useFileStore();
const authStore = useAuthStore();
const $showError = inject<IToastError>("$showError")!;
const $showSuccess = inject<IToastSuccess>("$showSuccess")!;

const props = defineProps<{
  isMobile: boolean;
}>();

const fileInput = ref<HTMLInputElement | null>(null);
const isImporting = ref(false);
const isDownloading = ref(false);
const isCollapsed = ref(true); // 移动端默认折叠
const showImportWarning = ref(false);

const shouldHide = computed(() => {
  // 移动端：未选中文件时显示，选中文件时隐藏
  // 桌面端：一直显示
  if (props.isMobile) {
    return fileStore.selectedCount > 0;
  }
  return false;
});

const toggleCollapsed = () => {
  isCollapsed.value = !isCollapsed.value;
};

const confirmImportBackup = () => {
  showImportWarning.value = true;
};

const proceedImport = () => {
  showImportWarning.value = false;
  importBackup();
};

const importBackup = () => {
  if (fileInput.value) {
    fileInput.value.click();
  }
};

const handleFileSelect = async (event: Event) => {
  const input = event.target as HTMLInputElement;
  if (!input.files || input.files.length === 0) return;

  const file = input.files[0];
  if (!file.name.toLowerCase().endsWith(".zip")) {
    $showError(t("seal.invalidFileType"));
    return;
  }

  isImporting.value = true;

  try {
    // 步骤1: 上传备份文件到临时位置
    const tempBackupPath = "/sealdice/_temp_backup.zip";
    await uploadFile(file, tempBackupPath);

    // 步骤2: 创建临时解压目录
    const tempExtractPath = "/sealdice/_temp_extract/";
    try {
      await fetchURL(`/api/resources${tempExtractPath}`, { method: "DELETE" });
    } catch (e) {
      // 忽略删除错误，目录可能不存在
    }

    // 步骤3: 解压备份文件到临时目录
    await fetchURL(`/api/extract${tempBackupPath}?destination=${encodeURIComponent(tempExtractPath)}`, {
      method: "POST",
    });

    // 步骤4: 删除旧的 data 目录
    try {
      await fetchURL("/api/resources/sealdice/data", { method: "DELETE" });
    } catch (e) {
      // 忽略删除错误，目录可能不存在
    }

    // 步骤5: 移动解压出的 data 目录到目标位置
    await fetchURL(
      `/api/resources/sealdice/_temp_extract/data?action=rename&destination=${encodeURIComponent("/sealdice/data")}`,
      { method: "PATCH" }
    );

    // 步骤6: 读取并修改 dice.yaml
    await updateDiceYaml();

    // 步骤7: 清理临时文件
    try {
      await fetchURL(`/api/resources${tempBackupPath}`, { method: "DELETE" });
      await fetchURL(`/api/resources${tempExtractPath}`, { method: "DELETE" });
    } catch (e) {
      // 忽略清理错误
    }

    $showSuccess(t("seal.importSuccess"));
    fileStore.reload = true;
  } catch (error: any) {
    console.error("Import backup error:", error);
    $showError(error);
  } finally {
    isImporting.value = false;
    // 清空文件输入
    if (input) {
      input.value = "";
    }
  }
};

const uploadFile = async (file: File, path: string): Promise<void> => {
  const response = await fetch(`${baseURL}/api/resources${path}?override=true`, {
    method: "POST",
    body: file,
    headers: {
      "X-Auth": authStore.jwt,
    },
  });

  if (!response.ok) {
    throw new Error(`Upload failed: ${response.statusText}`);
  }
};

const updateDiceYaml = async () => {
  try {
    // 读取 dice.yaml
    const response = await fetchURL("/api/resources/sealdice/data/dice.yaml", {});
    const fileInfo = await response.json();
    let content = fileInfo.content || "";

    // 检查并修改 serveAddress
    if (content.includes("serveAddress:")) {
      // 使用正则替换 serveAddress
      content = content.replace(
        /serveAddress:\s*['"]?[^'"\n]+['"]?/,
        'serveAddress: "0.0.0.0:3211"'
      );
    } else {
      // 如果没有 serveAddress，添加一个
      content = `serveAddress: "0.0.0.0:3211"\n${content}`;
    }

    // 保存修改后的文件
    await fetchURL("/api/resources/sealdice/data/dice.yaml", {
      method: "PUT",
      body: content,
    });
  } catch (e) {
    console.warn("Failed to update dice.yaml:", e);
    // 不抛出错误，因为文件可能不存在
  }
};

const downloadLatestBackup = async () => {
  isDownloading.value = true;

  try {
    // 获取 backups 目录的文件列表
    const response = await fetchURL("/api/resources/sealdice/backups/", {});
    const data = await response.json();

    if (!data.items || data.items.length === 0) {
      $showError(t("seal.noBackupsFound"));
      return;
    }

    // 过滤出备份文件 (bak_*.zip)
    const backupFiles = data.items.filter(
      (item: any) => !item.isDir && item.name.startsWith("bak_") && item.name.endsWith(".zip")
    );

    if (backupFiles.length === 0) {
      $showError(t("seal.noBackupsFound"));
      return;
    }

    // 按修改时间排序，获取最新的
    backupFiles.sort(
      (a: any, b: any) => new Date(b.modified).getTime() - new Date(a.modified).getTime()
    );

    const latestBackup = backupFiles[0];

    // 下载最新备份
    api.download(null, `/files/sealdice/backups/${latestBackup.name}`);
  } catch (error: any) {
    console.error("Download backup error:", error);
    $showError(error);
  } finally {
    isDownloading.value = false;
  }
};

const viewBackups = () => {
  router.push("/files/sealdice/backups/");
};
</script>

<style scoped>
/* 移动端折叠按钮 */
.seal-toggle-btn {
  position: fixed;
  bottom: 1em;
  right: 1em;
  z-index: 101;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  border: none;
  background: var(--blue);
  color: white;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.2);
  transition: transform 0.2s ease, opacity 0.2s ease;
}

.seal-toggle-btn:hover {
  background: var(--blueDark);
}

.seal-toggle-btn i {
  font-size: 24px;
}

.seal-toggle-btn.collapsed {
  opacity: 0.9;
}

#seal-toolbar {
  position: fixed;
  bottom: 1em;
  left: 50%;
  transform: translateX(-50%);
  z-index: 100;
  transition: opacity 0.2s ease, transform 0.2s ease;
}

#seal-toolbar.hidden {
  opacity: 0;
  pointer-events: none;
  transform: translateX(-50%) translateY(100%);
}

#seal-toolbar.collapsed {
  opacity: 0;
  pointer-events: none;
  transform: translateY(100%);
}

.seal-toolbar-content {
  display: flex;
  align-items: center;
  background: var(--surfaceSecondary);
  border-radius: 8px;
  padding: 0.5em 1em;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
  gap: 1em;
  flex-wrap: nowrap;
}

.seal-toolbar-title {
  font-weight: 500;
  color: var(--textPrimary);
  white-space: nowrap;
  padding-right: 0.5em;
  border-right: 1px solid var(--borderPrimary);
  flex-shrink: 0;
}

.seal-toolbar-actions {
  display: flex;
  gap: 0.5em;
  flex-shrink: 0;
}

.seal-action {
  display: flex;
  align-items: center;
  gap: 0.4em;
  padding: 0.4em 0.8em;
  border: none;
  background: var(--blue);
  color: white;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.9em;
  transition: background 0.2s ease;
  white-space: nowrap;
  flex-shrink: 0;
}

.seal-action:hover:not(:disabled) {
  background: var(--blueDark);
}

.seal-action:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.seal-action i {
  font-size: 1.2em;
  flex-shrink: 0;
}

.seal-action span {
  white-space: nowrap;
}

/* 警告弹框样式 */
.seal-warning-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
}

.seal-warning-dialog {
  background: var(--surfaceSecondary);
  border-radius: 8px;
  max-width: 480px;
  width: 90%;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.3);
}

.seal-warning-header {
  display: flex;
  align-items: center;
  gap: 0.5em;
  padding: 1em;
  border-bottom: 1px solid var(--borderPrimary);
  font-weight: 600;
  font-size: 1.1em;
  color: var(--textPrimary);
}

.seal-warning-header .warning-icon {
  color: #f59e0b;
  font-size: 1.5em;
}

.seal-warning-content {
  padding: 1em;
  color: var(--textPrimary);
}

.seal-warning-content p {
  margin-bottom: 0.8em;
}

.seal-warning-content ul {
  margin: 0;
  padding-left: 1.5em;
}

.seal-warning-content li {
  margin-bottom: 0.5em;
  color: var(--textSecondary);
}

.seal-warning-disclaimer {
  margin-top: 1em;
  padding: 0.5em;
  background: rgba(220, 38, 38, 0.1);
  border-left: 3px solid #dc2626;
  color: #dc2626;
  font-weight: 500;
}

.seal-warning-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5em;
  padding: 1em;
  border-top: 1px solid var(--borderPrimary);
}

.seal-warning-btn {
  padding: 0.5em 1.2em;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.9em;
  transition: background 0.2s ease;
}

.seal-warning-btn.cancel {
  background: var(--surfacePrimary);
  color: var(--textPrimary);
  border: 1px solid var(--borderPrimary);
}

.seal-warning-btn.cancel:hover {
  background: var(--surfaceSecondary);
}

.seal-warning-btn.confirm {
  background: #dc2626;
  color: white;
}

.seal-warning-btn.confirm:hover {
  background: #b91c1c;
}

/* 移动端样式 */
@media (max-width: 736px) {
  .seal-toggle-btn {
    bottom: 1em;
    right: 1em;
  }

  #seal-toolbar {
    bottom: 4.5em;
    width: calc(100% - 2em);
    max-width: none;
    left: 1em;
    right: 1em;
    transform: none;
  }

  #seal-toolbar.collapsed {
    transform: translateY(20px);
  }

  .seal-toolbar-content {
    flex-wrap: wrap;
    justify-content: center;
    padding: 0.6em;
    gap: 0.5em;
    border-radius: 8px;
  }

  .seal-toolbar-title {
    width: 100%;
    text-align: center;
    padding-right: 0;
    border-right: none;
    border-bottom: 1px solid var(--borderPrimary);
    padding-bottom: 0.5em;
  }

  .seal-toolbar-actions {
    width: 100%;
    justify-content: center;
    flex-wrap: wrap;
  }

  .seal-action {
    flex: 1;
    min-width: auto;
    justify-content: center;
    padding: 0.5em;
    font-size: 0.8em;
  }

  .seal-action span {
    display: inline;
  }
}
</style>
