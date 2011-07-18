#include "qsqleditor.h"
#include "ui_qsqleditor.h"

#include <QStandardItemModel>
#include <QStandardItem>
#include <QMenu>
#include <QAction>
#include <tableviewwidget.h>

QSqlEditor::QSqlEditor(LiteApi::IApplication *app) :
    m_liteApp(app),
    m_file(0),
    m_widget(new QWidget),
    ui(new Ui::QSqlEditor),
    m_bReadOnly(false)
{
    ui->setupUi(m_widget);
    m_dbModel = new QStandardItemModel(this);
    ui->dbTreeView->setEditTriggers(0);
    ui->dbTreeView->setModel(m_dbModel);
    ui->dbTreeView->setHeaderHidden(true);
    ui->dbTreeView->setContextMenuPolicy(Qt::CustomContextMenu);

    ui->tabWidget->setTabsClosable(true);

    m_infoAct = new QAction(tr("Infomation"),this);
    m_editorAct = new QAction(tr("Edit Table"),this);

    m_tableMenu = new QMenu;
    m_tableMenu->addAction(m_infoAct);
    m_tableMenu->addAction(m_editorAct);

    connect(ui->dbTreeView,SIGNAL(customContextMenuRequested(QPoint)),this,SLOT(dbTreeContextMenuRequested(QPoint)));
    connect(m_editorAct,SIGNAL(triggered()),this,SLOT(editorTable()));
    //connect(ui->tabWidget,SIGNAL(tabCloseRequested(int)),this,SLOT())
}

QSqlEditor::~QSqlEditor()
{
    ui->tabWidget->clear();
    if (m_file) {
        delete m_file;
    }
    delete m_tableMenu;
    delete ui;
    delete m_widget;
}

QWidget *QSqlEditor::widget()
{
    return m_widget;
}

QString QSqlEditor::displayName() const
{
    if (m_file) {
        return m_file->fileName();
    }
    return QString("");
}

bool QSqlEditor::open(const QString &fileName,const QString &mimeType)
{
    return false;
}

bool QSqlEditor::save()
{
    return false;
}

void QSqlEditor::setReadOnly(bool b)
{
    m_bReadOnly = b;
}

bool QSqlEditor::isReadOnly() const
{
    return m_bReadOnly;
}

bool QSqlEditor::isModified() const
{
    return false;
}

LiteApi::IFile *QSqlEditor::file()
{
    return m_file;
}

void QSqlEditor::setFile(QSqlDbFile *file)
{
    m_file = file;
    if (m_file && m_file->db().isOpen()) {
        loadDatabase();
    }
}

QString QSqlEditor::tableTypeNames(int type) const{
    if (type == QSql::Tables) {
        return tr("Tables");
    } else if (type == QSql::Views) {
        return tr("Views");
    } else if (type == QSql::SystemTables) {
        return tr("SystemTables");
    } else if (type == QSql::AllTables) {
        return tr("AllTables");
    }
    return tr("Unknow");
}

QString QSqlEditor::tableTypeName(int type) const{
    if (type == QSql::Tables) {
        return tr("Table");
    } else if (type == QSql::Views) {
        return tr("View");
    } else if (type == QSql::SystemTables) {
        return tr("SystemTable");
    } else if (type == QSql::AllTables) {
        return tr("AllTable");
    }
    return tr("Unknow");
}

void QSqlEditor::loadDatabase()
{
    m_dbModel->clear();
    appendTableItems(QSql::Tables);
    appendTableItems(QSql::Views);
    appendTableItems(QSql::SystemTables);
    ui->dbTreeView->expandAll();
}

void QSqlEditor::appendTableItems(QSql::TableType type)
{
    QString typeName = tableTypeNames(type);
    QStringList tables = m_file->db().tables(type);
    QStandardItem *root = new QStandardItem(typeName);
    foreach (QString table, tables) {
        QStandardItem *item = new QStandardItem(table);
        item->setData(type);
        root->appendRow(item);
    }
    m_dbModel->appendRow(root);
}

void QSqlEditor::dbTreeContextMenuRequested(const QPoint &pt)
{
    QModelIndex index = ui->dbTreeView->indexAt(pt);
    m_contextIndex = index;
    if (!index.isValid()) {
        return;
    }
    bool bok;
    int type = index.data(Qt::UserRole+1).toInt(&bok);
    QMenu *contextMenu = 0;
    if (type == QSql::Tables ||
            type == QSql::Views ||
            type == QSql::SystemTables) {
        contextMenu = m_tableMenu;
    }
    if (contextMenu) {
        contextMenu->popup(ui->dbTreeView->mapToGlobal(pt));
    }
}

void QSqlEditor::editorTable()
{
    int type = m_contextIndex.data(Qt::UserRole+1).toInt();
    if (type == QSql::Tables ||
            type == QSql::Views ||
            type == QSql::SystemTables) {
        QString typeName = tableTypeName(type);
        QString table = m_contextIndex.data().toString();
        TableViewWidget *w = new TableViewWidget(m_file->db());
        w->setTable(table);
        ui->tabWidget->addTab(w,QString(tr("%1:%2")).arg(typeName).arg(table));
        ui->tabWidget->setCurrentWidget(w);
    }
}