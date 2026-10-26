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

window.addEventListener('DOMContentLoaded',(function(e){
	var editor = {
		elem: document.getElementById('editor'),
		getValue: function(){return editor.elem.value;},
		setValue: function(value){editor.elem.value = value;}
	};
	var status = document.getElementById('save-status');
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

	var pagePath = document.getElementById('pagepath').value;
	var load = requestJson('GET', pageApiUrl(pagePath), function(json, xhr) {
		if (xhr.status == 200) {
			editor.setValue(xhr.responseText);
			update();
			status.textContent = '';
		} else if (xhr.status == 404) {
			// A missing page is a valid starting point for creating a new page.
			editor.setValue('');
			update();
			status.textContent = 'New page';
		} else {
			status.textContent = (json && json.message) || 'Failed to load page';
		}
	});
	load.send();

	document.getElementById('save-button').addEventListener('click', function(e){
		e.preventDefault();

		var pagePath = document.getElementById('pagepath').value;
		var save = requestJson('POST', pageApiUrl(pagePath), function(json, xhr) {
			if (xhr.status >= 200 && xhr.status < 300) {
				status.textContent = 'Saved';
			} else {
				status.textContent = (json && json.message) || 'Failed to save page';
			}
		});
		save.setRequestHeader('Content-Type', 'application/json');
		save.send(JSON.stringify({"text": editor.getValue()}));
	});
	
}),false);
