"use strict";

var pageApi = "/api/pages/"

function requestJson(method, url, f){
	var xhr = new XMLHttpRequest();
	xhr.open(method, url, true);
	xhr.onreadystatechange = function() {
		if (xhr.readyState != 4) return;
		var json = null;
		try {
			json = JSON.parse(xhr.responseText);
		} catch(e) {}
		if (f) {
			f(json, xhr)
		}
	};
	return xhr;
}

function pageApiUrl(pagePath) {
	pagePath = (pagePath || '').trim().replace(/^\/+/, '');
	if (pagePath == '') return pageApi;
	return pageApi + pagePath.split('/').map(encodeURIComponent).join('/');
}

function sectionApiUrl(pagePath, sectionName) {
	var url = pageApiUrl(pagePath);
	if ((sectionName || '').trim() == '') return url;
	return url + '?section=' + encodeURIComponent(sectionName.trim());
}

function pageDirectory(pagePath) {
	var normalized = (pagePath || '').trim().replace(/^\/+|\/+$/g, '');
	if (normalized == '') return '';
	var parts = normalized.split('/');
	parts.pop();
	return parts.join('/');
}

function fileApiUrl(directory, filename) {
	var parts = ['/api/files'];
	if ((directory || '') != '') {
		parts = parts.concat(directory.split('/'));
	}
	parts.push(filename);
	return parts.map(function(part, index) {
		return index == 0 ? part : encodeURIComponent(part);
	}).join('/');
}

function markdownLinkForFile(filename) {
	var imageExtensions = /\.(?:apng|avif|bmp|gif|heic|heif|ico|jpe?g|jxl|png|svg|tif?f|webp)$/i;
	var escapedFilename = filename.replace(/[\\\[\]()]/g, '\\$&');
	var linkTarget = encodeURIComponent(filename);
	var prefix = imageExtensions.test(filename) ? '!' : '';
	return prefix + '[' + escapedFilename + '](' + linkTarget + ')';
}

window.addEventListener('DOMContentLoaded',(function(e){
	var editor = {
		elem: document.getElementById('editor'),
		getValue: function(){return editor.elem.value;},
		setValue: function(value){editor.elem.value = value;}
	};
	var status = document.getElementById('save-status');
	var sectionInput = document.getElementById('sectionpath');
	var sectionUpdateUnixMS = 0;
	var update = function() {
		var dom = markup(editor.getValue());
		var e = document.getElementById('preview-pane');
		element_clear(e);
		element_append(e, dom);
	};

	if (window.CodeMirror) {
		editor = CodeMirror.fromTextArea(document.getElementById('editor'));
		editor.on("change", update);
	} else {
		editor.elem.addEventListener('change', update);
		editor.elem.addEventListener('keyup', update);
	}
	update();

	var pagePath = document.getElementById('pagepath').textContent.trim();
	var uploadDirectory = pageDirectory(pagePath);
	var uploadButton = document.getElementById('upload-button');
	var uploadInput = document.getElementById('file-upload');
	var dropZone = document.getElementById('file-drop-zone');
	var uploadSelection = null;

	var getEditorSelection = function() {
		if (editor.getCursor) {
			return {
				from: editor.getCursor('from'),
				to: editor.getCursor('to')
			};
		}
		return {
			start: editor.elem.selectionStart,
			end: editor.elem.selectionEnd
		};
	};
	var insertAtEditorSelection = function(text, selection) {
		if (editor.replaceSelection) {
			if (selection) editor.setSelection(selection.from, selection.to);
			editor.replaceSelection(text);
			return;
		}
		var elem = editor.elem;
		var start = selection ? selection.start : elem.selectionStart;
		var end = selection ? selection.end : elem.selectionEnd;
		editor.setValue(elem.value.substring(0, start) + text + elem.value.substring(end));
		elem.focus();
		elem.selectionStart = elem.selectionEnd = start + text.length;
	};

	var uploadFile = function(file) {
		if (!file || !file.name) return;
		var selection = uploadSelection || getEditorSelection();
		var xhr = new XMLHttpRequest();
		xhr.open('PUT', fileApiUrl(uploadDirectory, file.name), true);
		xhr.setRequestHeader('Content-Type', file.type || 'application/octet-stream');
		status.textContent = 'Uploading ' + file.name + '...';
		xhr.onload = function() {
			var json = null;
			try {
				json = JSON.parse(xhr.responseText);
			} catch (error) {}
			if (xhr.status >= 200 && xhr.status < 300) {
				insertAtEditorSelection(markdownLinkForFile(file.name), selection);
				update();
				status.textContent = 'Uploaded ' + file.name + '. Save to apply the link.';
			} else {
				status.textContent = (json && json.message) || 'Failed to upload file';
			}
			uploadSelection = null;
		};
		xhr.onerror = function() {
			status.textContent = 'Failed to upload file';
			uploadSelection = null;
		};
		xhr.send(file);
	};

	uploadButton.addEventListener('click', function() {
		uploadSelection = getEditorSelection();
		uploadInput.click();
	});
	uploadInput.addEventListener('change', function() {
		if (uploadInput.files.length > 0) {
			uploadFile(uploadInput.files[0]);
		} else {
			uploadSelection = null;
		}
		uploadInput.value = '';
	});
	dropZone.addEventListener('dragover', function(event) {
		event.preventDefault();
		dropZone.classList.add('file-drop-active');
	});
	dropZone.addEventListener('dragleave', function() {
		dropZone.classList.remove('file-drop-active');
	});
	dropZone.addEventListener('drop', function(event) {
		event.preventDefault();
		dropZone.classList.remove('file-drop-active');
		if (event.dataTransfer.files.length > 0) uploadFile(event.dataTransfer.files[0]);
	});
	dropZone.addEventListener('keydown', function(event) {
		if (event.key == 'Enter' || event.key == ' ') {
			event.preventDefault();
			uploadButton.click();
		}
	});

	var loadSection = function() {
		var sectionName = sectionInput.value.trim();
		var load = requestJson('GET', sectionApiUrl(pagePath, sectionName), function(json, xhr) {
		if (xhr.status == 200) {
			if (sectionName != '') {
				sectionUpdateUnixMS = (json && json.updateUnixMS) || 0;
				editor.setValue((json && json.text) || '');
			} else {
				sectionUpdateUnixMS = 0;
				editor.setValue(xhr.responseText);
			}
			update();
			status.textContent = '';
		} else if (xhr.status == 404) {
			// A missing page is a valid starting point for creating a new page.
			editor.setValue('');
			sectionUpdateUnixMS = 0;
			update();
			status.textContent = sectionName != '' ? 'New section' : 'New page';
		} else {
			status.textContent = (json && json.message) || 'Failed to load page';
		}
	});
		load.send();
	};
	loadSection();
	sectionInput.addEventListener('change', loadSection);

	document.getElementById('save-button').addEventListener('click', function(e){
		e.preventDefault();

		var pagePath = document.getElementById('pagepath').textContent.trim();
		var sectionName = sectionInput.value.trim();
		var save = requestJson('POST', sectionApiUrl(pagePath, sectionName), function(json, xhr) {
			if (xhr.status >= 200 && xhr.status < 300) {
				status.textContent = 'Saved';
			} else {
				status.textContent = (json && json.message) || 'Failed to save page';
			}
		});
		save.setRequestHeader('Content-Type', 'application/json');
		var payload = {"text": editor.getValue()};
		if (sectionName != '') payload.updateUnixMS = sectionUpdateUnixMS;
		save.send(JSON.stringify(payload));
	});
	
}),false);
