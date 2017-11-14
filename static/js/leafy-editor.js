"use strict";

var pageApi = "/api/pages/"

function requestJson(method, url, f){
	var xhr = new XMLHttpRequest();
	xhr.open(method, url);
	xhr.onreadystatechange = function() {
		if (xhr.readyState != 4) return;
		var json;
		try {
			JSON.parse(xhr.responseText);
		} catch(e) {}
		if (f) {
			f(json, xhr)
		}
	};
	return xhr;
}

function getJson(url,f){
	requestJson('GET', url, f).send();
}

window.addEventListener('DOMContentLoaded',(function(e){
	console.log("Hello, Leafy.");
	var editor = {
		elem: document.getElementById('editor'),
		getValue: function(){return editor.elem.value;}
	};
	var update = function() {
		var dom = markup(editor.getValue());
		var e = document.getElementById('preview-pane');
		element_clear(e);
		element_append(e, dom);
	};

	if (window.CodeMirror) {
		editor = CodeMirror.fromTextArea(document.getElementById('editor'));
		editor.on("change", update);
		update();
	} else {
		editor.elem.addEventListener('change', update);
		editor.elem.addEventListener('keyup', update);
		update();
	}

	document.getElementById('save-button').addEventListener('click', function(e){
		e.preventDefault();

		console.log("send");
		var pagePath = document.getElementById('pagepath').value;
		requestJson('POST', pageApi + pagePath, null).send(JSON.stringify({"text": editor.getValue()}));
	});
	
}),false);
