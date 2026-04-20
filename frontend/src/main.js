import { Events, Call } from '@wailsio/runtime';
import {
    GetConfig,
    ProcessFile,
    CheckConflicts,
    UndoAction,
    SelectDirectory,
    SelectWatchDirectory,
    SaveSetup,
    AddRepository,
    RemoveRepository,
    SetDefaultRepository,
    SkipFile,
    HandleDroppedItem,
    HideWindow,
    GetItemSummary,
    GuessCategory,
    ToggleAutoStart,
    AddCategory,
    RenameCategory,
    DeleteCategory
} from '../bindings/kicad-lib-mgr/app.js';

const setupView = document.getElementById('setup-view');
const mainView = document.getElementById('main-view');
const settingsView = document.getElementById('settings-view');
const conflictView = document.getElementById('conflict-view');

const libPathInput = document.getElementById('lib-path-input');
const btnBrowse = document.getElementById('btn-browse');
const btnSaveSetup = document.getElementById('btn-save-setup');

const mainTitle = document.getElementById('main-title');
const filenameDisplay = document.getElementById('filename-display');
const repositorySelect = document.getElementById('repository-select');
const selectCategory = document.getElementById('category-select');
const newCategoryInput = document.getElementById('new-category-input');
const btnSettings = document.getElementById('btn-settings');
const btnOk = document.getElementById('btn-ok');

const watchDirInput = document.getElementById('watch-dir-input');
const btnBrowseWatch = document.getElementById('btn-browse-watch');
const repoList = document.getElementById('repo-list');
const syncStatusIcon = document.getElementById('sync-status-icon');
const btnSyncNow = document.getElementById('btn-sync-now');
const autostartToggle = document.getElementById('autostart-toggle');
const historyList = document.getElementById('history-list');
const btnSettingsBack = document.getElementById('btn-settings-back');

// Conflict UI Elements
const conflictText = document.getElementById('conflict-text');
const conflictNewName = document.getElementById('conflict-new-name');
const btnConflictCancel = document.getElementById('btn-conflict-cancel');
const btnConflictProceed = document.getElementById('btn-conflict-proceed');

// UI Toggle Elements (New)
const navTabLibs = document.getElementById('nav-tab-libs');
const navTabCats = document.getElementById('nav-tab-cats');
const navTabSys = document.getElementById('nav-tab-sys');
const navTabHelp = document.getElementById('nav-tab-help');
const contentLibs = document.getElementById('content-libs');
const contentCats = document.getElementById('content-cats');
const contentSys = document.getElementById('content-sys');
const contentHelp = document.getElementById('content-help');
const btnShowAddRepo = document.getElementById('btn-show-add-repo');
const addRepoContainer = document.getElementById('add-repo-container');
const tabLocal = document.getElementById('tab-local');
const tabGit = document.getElementById('tab-git');
const formLocal = document.getElementById('form-local');
const formGit = document.getElementById('form-git');

// Form Inputs (Existing logic hooks here)
const newLocalName = document.getElementById('new-local-name');
const btnAddLocal = document.getElementById('btn-add-local');
const newGitUrl = document.getElementById('new-git-url');
const newGitName = document.getElementById('new-git-name');
const btnAddGit = document.getElementById('btn-add-git');

const categorySettingsList = document.getElementById('category-settings-list');
const btnShowAddCategory   = document.getElementById('btn-show-add-category');
const addCategoryContainer = document.getElementById('add-category-container');
const newCategoryName      = document.getElementById('new-category-name');
const categoryAddError     = document.getElementById('category-add-error');
const btnAddCategory       = document.getElementById('btn-add-category');
const toast                = document.getElementById('toast');

let _toastTimer = null;
function showToast(message, type = 'error') {
    const colors = { error: '#ff5555', success: '#50fa7b', info: '#bd93f9' };
    toast.style.borderLeftColor = colors[type] ?? colors.error;
    toast.innerText = formatWailsError(message);
    toast.style.display = 'block';
    clearTimeout(_toastTimer);
    _toastTimer = setTimeout(() => { toast.style.display = 'none'; }, 4000);
}

// Queue system to handle rapid downloads or multi-file drops
let fileQueue = [];
let currentConfig = null;

document.addEventListener("DOMContentLoaded", () => {
    setTimeout(() => {
        loadConfig().catch(err => {
            console.error("Critical initialization failure:", err);
            document.body.innerHTML = `<h2 style="color:#ff5555; padding:20px;">Startup Error: Failed to connect to background service.</h2>`;
        });
    }, 100);
});

async function loadConfig() {
    try {
        currentConfig = await GetConfig();
        
        if (!currentConfig.baseLibPath) {
            switchView(setupView);
        } else {
            if (settingsView.classList.contains('hidden') && conflictView.classList.contains('hidden') && fileQueue.length === 0) {
                switchView(mainView);
            }
            watchDirInput.value = currentConfig.watchDir || "";
            autostartToggle.checked = currentConfig.autoStart || false;
            populateCategories(currentConfig.categories || []);
            populateRepositories(currentConfig.repositories || []);
            populateCategorySettings(currentConfig.categories || []);
            populateHistory(currentConfig.history || []);
        }
    } catch (err) {
        console.error("Failed to load config:", err);
    }
}

function switchView(activeView) {
    [setupView, mainView, settingsView, conflictView].forEach(view => {
        if(view) view.classList.add('hidden');
    });
    activeView.classList.remove('hidden');
}

function populateCategories(categories) {
    const previousSelection = selectCategory.value;
    selectCategory.innerHTML = "";
    
    [...categories].sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase())).forEach(cat => {
        let opt = document.createElement('option');
        opt.value = cat;
        opt.innerText = cat;
        selectCategory.appendChild(opt);
    });

    let optNew = document.createElement('option');
    optNew.value = "ADD_NEW";
    optNew.innerText = "+ Add New Category...";
    selectCategory.appendChild(optNew);

    if (previousSelection && previousSelection !== "ADD_NEW") {
        selectCategory.value = previousSelection;
    }
}

function populateRepositories(repositories) {
    repositorySelect.innerHTML = "";
    repoList.innerHTML = "";

    const defaultRepo = currentConfig && currentConfig.defaultRepo;

    repositories.forEach(repo => {
        let opt = document.createElement('option');
        opt.value = repo.name;
        opt.innerText = !repo.url ? `${repo.name} (Local Folder)` : repo.name;
        repositorySelect.appendChild(opt);

        let li = document.createElement('li');
        li.style.display = "flex";
        li.style.justifyContent = "space-between";
        li.style.alignItems = "center";
        li.style.gap = "6px";

        let info = document.createElement('div');
        info.style.flex = "1";
        const isDefault = repo.name === defaultRepo;
        info.innerHTML = `<strong>${isDefault ? '★ ' : ''}${repo.name}</strong> <span style="color: var(--text-muted); font-size: 0.8rem;">${repo.url ? '(' + repo.url + ')' : '(Local)'}</span>`;

        let syncIcon = document.createElement('span');
        syncIcon.dataset.repoSyncIcon = repo.name;
        syncIcon.style.fontSize = "0.9rem";
        syncIcon.innerText = repo.url ? '☁️' : '';
        syncIcon.title = '';

        let starBtn = document.createElement('button');
        starBtn.className = 'btn-icon';
        starBtn.title = isDefault ? 'Default repo' : 'Set as default';
        starBtn.innerText = '⭐';
        starBtn.style.opacity = isDefault ? '1' : '0.4';
        starBtn.addEventListener('click', async () => {
            try {
                await SetDefaultRepository(repo.name);
                await loadConfig();
            } catch (err) {
                showToast('Failed to set default: ' + err);
            }
        });

        let removeBtn = document.createElement('button');
        removeBtn.className = 'btn-icon';
        removeBtn.title = 'Remove from app (files on disk are kept)';
        removeBtn.innerText = '🗑️';
        removeBtn.addEventListener('click', async () => {
            if (!confirm(`Remove "${repo.name}" from the app?\nFiles on disk will NOT be deleted.`)) return;
            try {
                await RemoveRepository(repo.name);
                await loadConfig();
            } catch (err) {
                showToast('Cannot remove: ' + err);
            }
        });

        li.appendChild(info);
        li.appendChild(syncIcon);
        li.appendChild(starBtn);
        li.appendChild(removeBtn);
        repoList.appendChild(li);
    });

    if (defaultRepo) {
        repositorySelect.value = defaultRepo;
    }
}

function populateCategorySettings(categories) {
    categorySettingsList.innerHTML = "";
    const sorted = [...categories].sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()));
    const isLast = sorted.length === 1;

    sorted.forEach(cat => {
        const li = document.createElement('li');
        li.style.cssText = "display:flex; justify-content:space-between; align-items:center; gap:6px;";

        const nameSpan = document.createElement('span');
        nameSpan.style.flex = "1";
        nameSpan.innerText = cat;

        const renameBtn = document.createElement('button');
        renameBtn.className = 'btn-icon';
        renameBtn.title = 'Rename category';
        renameBtn.innerText = '✏️';

        const deleteBtn = document.createElement('button');
        deleteBtn.className = 'btn-icon';
        deleteBtn.title = isLast ? 'Cannot delete the last category' : 'Delete category';
        deleteBtn.innerText = '🗑️';
        deleteBtn.disabled = isLast;
        deleteBtn.style.opacity = isLast ? '0.3' : '1';

        renameBtn.addEventListener('click', () => {
            const input = document.createElement('input');
            input.type = 'text';
            input.value = cat;
            input.style.cssText = "flex:1; padding:2px 6px; font-size:0.9rem; border-radius:4px; border:1px solid var(--border); background:var(--bg-color); color:white;";

            const saveBtn = document.createElement('button');
            saveBtn.className = 'btn-icon';
            saveBtn.title = 'Save';
            saveBtn.innerText = '✔';

            const cancelBtn = document.createElement('button');
            cancelBtn.className = 'btn-icon';
            cancelBtn.title = 'Cancel';
            cancelBtn.innerText = '✖';

            li.replaceChild(input, nameSpan);
            li.replaceChild(saveBtn, renameBtn);
            li.insertBefore(cancelBtn, deleteBtn);
            input.focus();
            input.select();

            const doRename = async () => {
                const newName = input.value.trim();
                if (!newName || newName === cat) { await loadConfig(); return; }
                try {
                    await RenameCategory(cat, newName);
                    await loadConfig();
                } catch (err) {
                    input.style.borderColor = '#ff5555';
                    input.title = String(err).replace(/^error:/i, '').trim();
                    setTimeout(() => {
                        input.style.borderColor = '';
                        input.title = '';
                    }, 4000);
                }
            };

            saveBtn.addEventListener('click', doRename);
            cancelBtn.addEventListener('click', () => loadConfig());
            input.addEventListener('keydown', (e) => {
                if (e.key === 'Enter')  doRename();
                if (e.key === 'Escape') loadConfig();
            });
        });

        deleteBtn.addEventListener('click', async () => {
            if (!confirm(`Delete category "${cat}"?\nThis only removes it from the list — existing files are not affected.`)) return;
            try {
                await DeleteCategory(cat);
                await loadConfig();
            } catch (err) {
                showToast('Delete failed: ' + err);
            }
        });

        li.appendChild(nameSpan);
        li.appendChild(renameBtn);
        li.appendChild(deleteBtn);
        categorySettingsList.appendChild(li);
    });
}

function populateHistory(historyItems) {
    historyList.innerHTML = "";
    if (!historyItems || historyItems.length === 0) {
        historyList.innerHTML = "<li class='history-item'><span class='history-text' style='color: var(--text-muted);'>No recent imports.</span></li>";
        return;
    }

    const reversed = [...historyItems].reverse();

    reversed.forEach((item, index) => {
        let li = document.createElement('li');
        li.className = 'history-item';
        
        let txt = document.createElement('span');
        txt.className = 'history-text';
        txt.innerText = `${item.filename} → ${item.category}`;
        txt.title = `Imported to ${item.repoName} on ${new Date(item.timestamp * 1000).toLocaleString()}`;
        
        li.appendChild(txt);

        // Only allow rollback on the very latest import to ensure sym-lib .bak integrity
        if (index === 0) {
            let btn = document.createElement('button');
            btn.className = 'btn-undo';
            btn.innerText = 'Rollback Last';
            btn.onclick = async () => {
                if (confirm(`Are you sure you want to rollback the most recent import: ${item.filename}?`)) {
                    const success = await UndoAction(item.id);
                    if (success) {
                        await loadConfig();
                    } else {
                        showToast("Undo failed — files may have been moved or deleted manually.");
                    }
                }
            };
            li.appendChild(btn);
        } else {
            // Just show the time for older items in the history log
            let timeSpan = document.createElement('span');
            timeSpan.style.color = "var(--text-muted)";
            timeSpan.style.fontSize = "0.75rem";
            timeSpan.innerText = new Date(item.timestamp * 1000).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'});
            li.appendChild(timeSpan);
        }

        historyList.appendChild(li);
    });
}

function resetStandbyUI() {
    mainTitle.innerText = "Monitoring Folder...";
    filenameDisplay.innerHTML = "Waiting for file...";
    
    newCategoryInput.value = "";
    newCategoryInput.classList.add('hidden');
    
    btnOk.disabled = true;
    btnOk.style.opacity = "0.5";
    btnOk.style.cursor = "not-allowed";
}

async function processNextInQueue() {
    if (fileQueue.length === 0) {
        resetStandbyUI();
        await HideWindow();
        return;
    }

    let currentFilename = fileQueue[0];
    const baseName = currentFilename.split(/[\\/]/).pop();
    
    mainTitle.innerText = "New KiCad Part Detected";
    filenameDisplay.innerHTML = `<strong>${baseName}</strong><br><small style="color: var(--text-muted);">Analyzing contents...</small>`;
    
    if (fileQueue.length > 1) {
        filenameDisplay.innerHTML += `<br><span style="color: var(--accent); font-weight: bold; font-size: 0.85rem; display: inline-block; margin-top: 4px;">Queue: ${fileQueue.length - 1} more file(s) waiting...</span>`;
    }
    
    await loadConfig();
    switchView(mainView);
    
    newCategoryInput.classList.add('hidden');
    newCategoryInput.value = "";
    
    btnOk.disabled = false;
    btnOk.style.opacity = "1";
    btnOk.style.cursor = "pointer";

    try {
        const guessedCategory = await GuessCategory(currentFilename);
        if (guessedCategory) {
            const options = Array.from(selectCategory.options).map(opt => opt.value);
            if (options.includes(guessedCategory)) {
                selectCategory.value = guessedCategory;
            }
        }
    } catch (err) {}

    try {
        const summary = await GetItemSummary(currentFilename);
        filenameDisplay.innerHTML = `<strong>${baseName}</strong><br><small style="color: var(--accent); opacity: 0.9;">${summary}</small>`;
        if (fileQueue.length > 1) {
            filenameDisplay.innerHTML += `<br><span style="color: var(--accent); font-weight: bold; font-size: 0.85rem; display: inline-block; margin-top: 4px;">Queue: ${fileQueue.length - 1} more file(s) waiting...</span>`;
        }
    } catch (err) {
        filenameDisplay.innerHTML = `<strong>${baseName}</strong>`;
    }
}

// UI Toggles (Tabs & Expanders)
function switchSettingsTab(activeBtn, activeContent) {
    [navTabLibs, navTabCats, navTabSys, navTabHelp].forEach(b => b.classList.remove('active'));
    [contentLibs, contentCats, contentSys, contentHelp].forEach(c => c.classList.remove('active'));
    activeBtn.classList.add('active');
    activeContent.classList.add('active');
}

function formatWailsError(err) {
    // 1. If it's a JS Error object, grab its message directly. Otherwise, cast to string.
    let msg = (err && err.message) ? err.message : String(err);

    // 2. Strip any leftover "Error:" prefixes just in case
    msg = msg.replace(/^error:\s*/i, '').trim();

    // 3. Now it's a clean string, try to parse the Wails JSON
    try {
        const parsed = JSON.parse(msg);
        if (parsed && parsed.message) {
            msg = parsed.message;
        }
    } catch (e) {
        // If it's not JSON, do nothing and fall through
    }
    
    // Capitalize the first letter for a polished look
    return msg.charAt(0).toUpperCase() + msg.slice(1);
}

navTabLibs.addEventListener('click', () => switchSettingsTab(navTabLibs, contentLibs));
navTabCats.addEventListener('click', () => switchSettingsTab(navTabCats, contentCats));
navTabSys.addEventListener('click', () => switchSettingsTab(navTabSys, contentSys));
navTabHelp.addEventListener('click', () => switchSettingsTab(navTabHelp, contentHelp));

btnShowAddRepo.addEventListener('click', () => {
    addRepoContainer.classList.toggle('hidden');
    const isHidden = addRepoContainer.classList.contains('hidden');
    btnShowAddRepo.innerText = isHidden ? "+ Add Repo" : "Collapse";
});

btnShowAddCategory.addEventListener('click', () => {
    addCategoryContainer.classList.toggle('hidden');
    const collapsed = addCategoryContainer.classList.contains('hidden');
    btnShowAddCategory.innerText = collapsed ? '+ Add Category' : 'Collapse';
    if (!collapsed) newCategoryName.focus();
});

tabLocal.addEventListener('click', () => {
    tabLocal.classList.add('active');
    tabGit.classList.remove('active');
    formLocal.classList.remove('hidden');
    formGit.classList.add('hidden');
});

tabGit.addEventListener('click', () => {
    tabGit.classList.add('active');
    tabLocal.classList.remove('active');
    formGit.classList.remove('hidden');
    formLocal.classList.add('hidden');
});

// Logic Event Listeners
Events.On("file-detected", async (e) => {
    const filename = Array.isArray(e.data) ? e.data[0] : e.data;
    fileQueue.push(filename);
    if (fileQueue.length === 1) {
        await processNextInQueue();
    }
});

Events.On("watcher-error", (e) => {
    const message = Array.isArray(e.data) ? e.data[0] : e.data;
    console.error("Watcher error:", message);
    const titleEl = document.getElementById('main-title');
    if (titleEl) {
        titleEl.innerText = "Watch Error";
        titleEl.style.color = "var(--error, #ff5555)";
    }
    const filenameEl = document.getElementById('filename-display');
    if (filenameEl) {
        filenameEl.innerHTML = `<small style="color: var(--error, #ff5555);">${message}<br>Check the watch folder in Settings.</small>`;
    }
});

Events.On("file-rejected", (e) => {
    const filename = Array.isArray(e.data) ? e.data[0] : e.data;
    const baseName = filename.split(/[\\/]/).pop();
    const overlay = document.getElementById('drag-overlay');
    const overlayText = overlay.querySelector('div');
    
    const originalText = overlayText.innerText;
    overlayText.innerText = `❌ No KiCad assets in: ${baseName}`;
    overlayText.style.color = '#ff5555';
    overlay.style.borderColor = '#ff5555';
    
    overlay.style.display = 'flex';
    document.body.classList.add('shake-animation');
    
    setTimeout(() => {
        overlay.style.display = '';
        overlayText.innerText = originalText;
        overlayText.style.color = '';
        overlay.style.borderColor = '';
        document.body.classList.remove('shake-animation');
    }, 2500);
});

autostartToggle.addEventListener('change', async (e) => {
    try {
        await ToggleAutoStart(e.target.checked);
    } catch (err) {
        console.error('AutoStart toggle failed:', err);
        e.target.checked = !e.target.checked; 
    }
});

btnBrowse.addEventListener('click', async () => {
    const selectedDir = await SelectDirectory();
    if (selectedDir) {
        libPathInput.value = selectedDir;
        btnSaveSetup.disabled = false;
    }
});

btnSaveSetup.addEventListener('click', async () => {
    const path = libPathInput.value;
    if (path) {
        try {
            await SaveSetup(path);
            await loadConfig();
            if (fileQueue.length === 0) await HideWindow();
        } catch (err) {
            showToast("Failed to save setup: " + err);
        }
    }
});

btnBrowseWatch.addEventListener('click', async () => {
    const selectedDir = await SelectWatchDirectory();
    if (selectedDir) {
        watchDirInput.value = selectedDir;
        await loadConfig();
    }
});

selectCategory.addEventListener('change', (e) => {
    if (e.target.value === "ADD_NEW") {
        newCategoryInput.classList.remove('hidden');
        newCategoryInput.focus();
    } else {
        newCategoryInput.classList.add('hidden');
    }
});

// Centralized strategy processing helper
async function processItemWithStrategy(strategy, newName) {
    let currentFilename = fileQueue[0];
    let chosenCategory = selectCategory.value === "ADD_NEW" ? newCategoryInput.value.trim() : selectCategory.value;
    let chosenRepo = repositorySelect.value;

    btnConflictProceed.disabled = true;
    btnOk.disabled = true;
    
    try {
        await ProcessFile(currentFilename, chosenCategory, chosenRepo, strategy, newName);
        fileQueue.shift(); 
        await loadConfig();
        selectCategory.value = chosenCategory; 
        await processNextInQueue(); 
    } catch (err) {
        showToast("Processing error: " + err);
    } finally {
        btnConflictProceed.disabled = false;
        btnOk.disabled = false;
    }
}

btnOk.addEventListener('click', async () => {
    if (fileQueue.length === 0) return;

    let currentFilename = fileQueue[0];
    let chosenCategory = selectCategory.value;
    let chosenRepo = repositorySelect.value;
    
    if (chosenCategory === "ADD_NEW") {
        chosenCategory = newCategoryInput.value.trim();
        if (chosenCategory === "") {
            newCategoryInput.style.borderColor = "red";
            setTimeout(() => newCategoryInput.style.borderColor = "", 2000);
            return;
        }
    }
    
    btnOk.disabled = true;

    try {
        const conflicts = await CheckConflicts(currentFilename, chosenCategory, chosenRepo);
        if (conflicts && conflicts.length > 0) {
            conflictText.innerHTML = `<strong>File collision detected:</strong><br>• ` + conflicts.join('<br>• ');
            document.querySelector('input[name="conflict-action"][value="overwrite"]').checked = true;
            conflictNewName.classList.add('hidden');
            conflictNewName.value = '';
            switchView(conflictView);
            btnOk.disabled = false; 
            return; 
        }

        // Fast path: No conflicts
        await processItemWithStrategy("overwrite", "");
    } catch (err) {
        showToast("Error verifying component: " + err);
        btnOk.disabled = false;
    }
});

// Conflict UI Event Listeners
document.querySelectorAll('input[name="conflict-action"]').forEach(radio => {
    radio.addEventListener('change', (e) => {
        if (e.target.value === 'rename') {
            conflictNewName.classList.remove('hidden');
            conflictNewName.focus();
        } else {
            conflictNewName.classList.add('hidden');
        }
    });
});

btnConflictProceed.addEventListener('click', async () => {
    const strategy = document.querySelector('input[name="conflict-action"]:checked').value;
    const newName = conflictNewName.value.trim();

    if (strategy === 'rename' && !newName) {
        conflictNewName.style.borderColor = 'red';
        setTimeout(() => conflictNewName.style.borderColor = '', 2000);
        return;
    }

    await processItemWithStrategy(strategy, newName);
});

btnConflictCancel.addEventListener('click', async () => {
    fileQueue.shift(); 
    await SkipFile(fileQueue[0]); 
    await processNextInQueue(); 
});


document.getElementById('btn-skip').addEventListener('click', async () => {
    if (fileQueue.length > 0) {
        await SkipFile(fileQueue[0]);
        fileQueue.shift(); 
        await processNextInQueue(); 
    }
});

document.getElementById('btn-cancel').addEventListener('click', async () => {
    fileQueue = []; 
    await HideWindow();
    resetStandbyUI(); 
});

btnSettings.addEventListener('click', () => {
    switchView(settingsView);
});

btnSettingsBack.addEventListener('click', () => {
    switchView(mainView);
});

btnAddLocal.addEventListener('click', async () => {
    const name = newLocalName.value.trim();
    if (!name) {
        newLocalName.style.borderColor = 'red';
        setTimeout(() => newLocalName.style.borderColor = '', 2000);
        return;
    }

    btnAddLocal.disabled = true;
    btnAddLocal.innerText = 'Creating folder...';
    try {
        await AddRepository(name, '');
        await loadConfig();
        newLocalName.value = '';
        addRepoContainer.classList.add('hidden');
        btnShowAddRepo.innerText = "+ Add Repo";
    } catch (err) {
        showToast('Failed to create library: ' + err);
    } finally {
        btnAddLocal.disabled = false;
        btnAddLocal.innerText = 'Create Local Library';
    }
});

btnAddGit.addEventListener('click', async () => {
    const url = newGitUrl.value.trim();
    const name = newGitName.value.trim();

    if (!url) {
        newGitUrl.style.borderColor = 'red';
        setTimeout(() => newGitUrl.style.borderColor = '', 2000);
        return;
    }
    if (!name) {
        newGitName.style.borderColor = 'red';
        setTimeout(() => newGitName.style.borderColor = '', 2000);
        return;
    }

    btnAddGit.disabled = true;
    btnAddGit.innerText = 'Validating URL...';
    syncStatusIcon.title = 'Validating repository URL...';

    try {
        btnAddGit.innerText = 'Cloning repository...';
        await AddRepository(name, url);
        await loadConfig();
        newGitUrl.value = '';
        newGitName.value = '';
        addRepoContainer.classList.add('hidden');
        btnShowAddRepo.innerText = "+ Add Repo";
    } catch (err) {
        showToast('Failed to connect library: ' + err);
    } finally {
        btnAddGit.disabled = false;
        btnAddGit.innerText = 'Validate & Clone';
    }
});

btnAddCategory.addEventListener('click', async () => {
    const name = newCategoryName.value.trim();
    if (!name) {
        newCategoryName.style.borderColor = 'red';
        setTimeout(() => newCategoryName.style.borderColor = '', 2000);
        return;
    }
    btnAddCategory.disabled = true;
    btnAddCategory.innerText = 'Adding...';
    try {
        await AddCategory(name);
        await loadConfig();
        newCategoryName.value = '';
        categoryAddError.style.display = 'none';
        addCategoryContainer.classList.add('hidden');
        btnShowAddCategory.innerText = '+ Add Category';
    } catch (err) {
        categoryAddError.innerText = formatWailsError(err);
        categoryAddError.style.display = 'inline';
        newCategoryName.style.borderColor = '#ff5555';
        setTimeout(() => {
            categoryAddError.style.display = 'none';
            newCategoryName.style.borderColor = '';
        }, 4000);
    } finally {
        btnAddCategory.disabled = false;
        btnAddCategory.innerText = 'Add Category';
    }
});

btnSyncNow.addEventListener('click', async () => {
    btnSyncNow.disabled = true;
    try {
        await Call.ByName('App.SyncAllRepositories');
    } catch (err) {
        console.error('Sync failed:', err);
    } finally {
        btnSyncNow.disabled = false;
    }
});

Events.On('sync-status', (e) => {
    const raw = Array.isArray(e.data) ? e.data[0] : e.data;
    syncStatusIcon.style.opacity = '1';

    if (raw === 'syncing') {
        syncStatusIcon.innerText = '🔄';
        syncStatusIcon.title = 'Syncing...';
        return;
    }

    try {
        const map = JSON.parse(raw);
        let hasWarning = false;
        for (const [name, state] of Object.entries(map)) {
            const el = document.querySelector(`[data-repo-sync-icon="${name}"]`);
            if (el) {
                el.innerText = state === 'warning' ? '⚠️' : '✅';
                el.title = state === 'warning' ? 'Behind remote or offline' : 'Up to date';
            }
            if (state === 'warning') hasWarning = true;
        }
        syncStatusIcon.innerText = hasWarning ? '⚠️' : '✅';
        syncStatusIcon.title = hasWarning ? 'One or more repos are offline or behind.' : 'All libraries up to date.';
    } catch {
        // Legacy plain-string fallback
        if (raw === 'synced') {
            syncStatusIcon.innerText = '✅';
            syncStatusIcon.title = 'All libraries up to date.';
        } else {
            syncStatusIcon.innerText = '⚠️';
            syncStatusIcon.title = 'Offline or un-synced local changes.';
        }
    }
});