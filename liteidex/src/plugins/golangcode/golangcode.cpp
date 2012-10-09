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
// Module: golangcode.cpp
// Creator: visualfc <visualfc@gmail.com>
// date: 2011-5-19
// $Id: golangcode.cpp,v 1.0 2011-7-25 visualfc Exp $

#include "golangcode.h"
#include "fileutil/fileutil.h"
#include <QProcess>
#include <QTextDocument>
#include <QAbstractItemView>
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

GolangCode::GolangCode(LiteApi::IApplication *app, QObject *parent) :
    QObject(parent),
    m_liteApp(app),
    m_completer(0)
{
    m_process = new QProcess(this);
    connect(m_process,SIGNAL(started()),this,SLOT(started()));
    connect(m_process,SIGNAL(finished(int,QProcess::ExitStatus)),this,SLOT(finished(int,QProcess::ExitStatus)));

    m_gocodeCmd = m_liteApp->settings()->value("golangcode/cmd").toString();
    m_bLoad = false;
    m_envManager = LiteApi::findExtensionObject<LiteApi::IEnvManager*>(m_liteApp,"LiteApi.IEnvManager");
    if (m_envManager) {
        connect(m_envManager,SIGNAL(currentEnvChanged(LiteApi::IEnv*)),this,SLOT(currentEnvChanged(LiteApi::IEnv*)));
        currentEnvChanged(m_envManager->currentEnv());
    }
    m_golangAst = LiteApi::findExtensionObject<LiteApi::IGolangAst*>(m_liteApp,"LiteApi.IGolangAst");
    connect(m_liteApp->editorManager(),SIGNAL(currentEditorChanged(LiteApi::IEditor*)),this,SLOT(currentEditorChanged(LiteApi::IEditor*)));
}

GolangCode::~GolangCode()
{
    if (!m_gocodeCmd.isEmpty()) {
        m_liteApp->settings()->setValue("golangcode/cmd",m_gocodeCmd);
        if (m_bLoad) {
            m_process->start(m_gocodeCmd,QStringList() << "close");
            m_process->waitForFinished(200);
        }
    }
    delete m_process;
}

void GolangCode::currentEnvChanged(LiteApi::IEnv*)
{
    QProcessEnvironment env = LiteApi::getGoEnvironment(m_liteApp);
    QString goroot = env.value("GOROOT");
    QString gobin = env.value("GOBIN");
    if (!goroot.isEmpty() && gobin.isEmpty()) {
        gobin = goroot+"/bin";
    }
    QString gocode = FileUtil::findExecute(gobin+"/gocode");
    if (gocode.isEmpty()) {
        gocode = FileUtil::lookPath("gocode",env,true);
    }
    if (!FileUtil::compareFile(m_gocodeCmd,gocode)) {
        if (!m_gocodeCmd.isEmpty()) {
            m_process->start(m_gocodeCmd,QStringList() << "close");
            m_process->waitForFinished(200);
            m_bLoad = false;
        }
    }
    m_process->setProcessEnvironment(env);
    m_gocodeCmd = gocode;
}

void GolangCode::currentEditorChanged(LiteApi::IEditor *editor)
{
    LiteApi::ITextEditor *ed = LiteApi::getTextEditor(editor);
    if (!ed) {
        return;
    }
    QString filePath = ed->filePath();
    if (filePath.isEmpty()) {
        return;
    }
    m_fileName = QFileInfo(filePath).fileName();
    m_process->setWorkingDirectory(QFileInfo(filePath).path());
}

void GolangCode::setCompleter(LiteApi::ICompleter *completer)
{
    if (m_completer) {
        disconnect(m_completer,0,this,0);
    }
    m_completer = completer;
    if (m_completer) {
        m_completer->setSearchSeparator(false);
        connect(m_completer,SIGNAL(prefixChanged(QTextCursor,QString)),this,SLOT(prefixChanged(QTextCursor,QString)));
        connect(m_completer,SIGNAL(wordCompleted(QString,QString)),this,SLOT(wordCompleted(QString,QString)));
    }
}

void GolangCode::prefixChanged(QTextCursor cur,QString pre)
{
    if (m_process->state() == QProcess::Running) {
        return;
    }
    if (m_gocodeCmd.isEmpty()) {
        return;
    }
    m_lastPrefix = m_prefix;
    qDebug() << "test->"<< pre;
    int last = pre.lastIndexOf(".");
    if (last < 0) {
        return;
    }

    m_prefix = pre.left(last+1);
    qDebug() << "test"<< m_prefix;

    QString src = cur.document()->toPlainText();
    src = src.replace("\r\n","\n");
    m_writeData = src.left(cur.position()).toUtf8();
    QStringList args;
    args << "-in" << "" << "-f" << "csv" << "autocomplete" << m_fileName << QString::number(m_writeData.length());
    m_writeData = src.toUtf8();

    m_process->start(m_gocodeCmd,args);
}

void GolangCode::wordCompleted(QString,QString)
{
    m_prefix.clear();
}

void GolangCode::started()
{
    m_bLoad = true;
    if (m_writeData.isEmpty()) {
        return;
    }
    m_process->write(m_writeData);
    m_process->closeWriteChannel();
    m_writeData.clear();
}

void GolangCode::finished(int code,QProcess::ExitStatus)
{
    if (code != 0) {
        return;
    }
    if (m_prefix.isEmpty()) {
        return;
    }

    if (m_prefix != m_lastPrefix) {
     //   m_prefix.clear();
     //   return;
    }

    QString read = m_process->readAllStandardOutput();
    QStringList all = read.split('\n');
    //func,,Fprint,,func(w io.Writer, a ...interface{}) (n int, error os.Error)
    //type,,Formatter,,interface
    //const,,ModeExclusive,,
    //var,,Args,,[]string
    int n = 0;
    QIcon icon;

    m_completer->clearItemChilds(m_prefix);

    foreach (QString s, all) {
        QStringList word = s.split(",,");
        if (word.count() != 3) {
            continue;
        }
        LiteApi::ASTTAG_ENUM tag = LiteApi::TagNone;
        QString kind = word.at(0);
        QString info = word.at(2);
        if (kind == "func") {
            tag = LiteApi::TagFunc;
        } else if (kind == "var") {
            tag = LiteApi::TagValue;
        } else if (kind == "const") {
            tag = LiteApi::TagConst;
        } else if (kind == "type") {
            if (info == "interface") {
                tag = LiteApi::TagInterface;
            } else if (info == "struct") {
                tag = LiteApi::TagStruct;
            } else {
                tag = LiteApi::TagType;
            }
        }
        if (m_golangAst) {
            icon = m_golangAst->iconFromTagEnum(tag,true);
        }
        qDebug() << m_prefix+word.at(1);
        if (m_completer->appendItemEx(m_prefix+word.at(1),kind,info,icon,true)) {
            n++;
        }
    }

    m_prefix.clear();
    if (n >= 1) {
        m_completer->completer()->model()->sort(0);
        m_completer->show();
    }
}
