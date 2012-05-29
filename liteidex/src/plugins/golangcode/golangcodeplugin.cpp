/**************************************************************************
** This file is part of LiteIDE
**
** Copyright (c) 2011 LiteIDE Team. All rights reserved.
**
** This library is free software; you can redistribute it and/or
** modify it under the terms of the GNU Lesser General Public
** License as published by the Free Software Foundation; either
** version 2.1 of the License, or (at your option) any later version.
**
** This library is distributed in the hope that it will be useful,
** but WITHOUT ANY WARRANTY; without even the implied warranty of
** MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the GNU
** Lesser General Public License for more details.
**
** In addition, as a special exception,  that plugins developed for LiteIDE,
** are allowed to remain closed sourced and can be distributed under any license .
** These rights are included in the file LGPL_EXCEPTION.txt in this package.
**
**************************************************************************/
// Module: golangcodeplugin.cpp
// Creator: visualfc <visualfc@gmail.com>
// date: 2011-5-19
// $Id: golangcodeplugin.cpp,v 1.0 2011-7-25 visualfc Exp $

#include "golangcodeplugin.h"
#include "liteapi/litefindobj.h"
#include "liteeditorapi/liteeditorapi.h"
#include "fileutil/fileutil.h"
#include "qtc_editutil/uncommentselection.h"
#include "golangcode.h"
#include <QMenu>
#include <QAction>
#include <QPlainTextEdit>
#include <QDebug>
//lite_memory_check_begin
#if defined(WIN32) && defined(_MSC_VER) &&  defined(_DEBUG)
     #define _CRTDBG_MAP_ALLOC
     #include <stdlib.h>
     #include <crtdbg.h>
     #define DEBUG_NEW new( _NORMAL_BLOCK, __FILE__, __LINE__ )
     #define new DEBUG_NEW
#endif
//lite_memory_check_end

GolangCodePlugin::GolangCodePlugin()
{
    m_info->setId("plugin/golangcode");
    m_info->setName("GolangCode");
    m_info->setAnchor("visualfc");
    m_info->setInfo("Golang Gocode Plugin");
}

bool GolangCodePlugin::initWithApp(LiteApi::IApplication *app)
{
    if (!LiteApi::IPlugin::initWithApp(app)) {
        return false;
    }

    m_code = new GolangCode(app,this);
    m_commentAct = new QAction(tr("Toggle Comment Selection"),this);
    connect(m_commentAct,SIGNAL(triggered()),this,SLOT(editorComment()));
    connect(m_liteApp->editorManager(),SIGNAL(editorCreated(LiteApi::IEditor*)),this,SLOT(editorCreated(LiteApi::IEditor*)));
    connect(m_liteApp->editorManager(),SIGNAL(currentEditorChanged(LiteApi::IEditor*)),this,SLOT(currentEditorChanged(LiteApi::IEditor*)));
    return true;
}

QStringList GolangCodePlugin::dependPluginList() const
{
    return QStringList() << "plugin/liteenv" << "plugin/golangast";
}

void GolangCodePlugin::editorCreated(LiteApi::IEditor *editor)
{
    if (editor && editor->mimeType() == "text/x-gosrc") {
        QMenu *menu = LiteApi::findExtensionObject<QMenu*>(editor,"LiteApi.ContextMenu");
        if (menu) {
            menu->addSeparator();
            menu->addAction(m_commentAct);
        }
    }
}

void GolangCodePlugin::editorComment()
{
    LiteApi::IEditor *editor = m_liteApp->editorManager()->currentEditor();
    if (!editor) {
        return;
    }
    QPlainTextEdit *textEdit = LiteApi::findExtensionObject<QPlainTextEdit*>(editor,"LiteApi.QPlainTextEdit");
    if (!textEdit) {
        return;
    }
    Utils::unCommentSelection(textEdit);
}

void GolangCodePlugin::currentEditorChanged(LiteApi::IEditor *editor)
{
    if (editor && editor->mimeType() == "text/x-gosrc") {
        LiteApi::ICompleter *completer = LiteApi::findExtensionObject<LiteApi::ICompleter*>(editor,"LiteApi.ICompleter");
        m_code->setCompleter(completer);
    } else {
        m_code->setCompleter(0);
    }
}

Q_EXPORT_PLUGIN(GolangCodePlugin)
